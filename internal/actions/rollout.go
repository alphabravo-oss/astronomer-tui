//revive:disable:exported package-comments
package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const revisionAnnotation = "deployment.kubernetes.io/revision"

// Revision is one rollout revision of a workload.
type Revision struct {
	Number int64
	Cause  string
	Name   string // backing RS or ControllerRevision name
}

// RolloutRevisions lists a workload's revisions, newest first.
func RolloutRevisions(ctx context.Context, client kubernetes.Interface, kind, namespace, name string) ([]Revision, error) {
	switch kind {
	case "Deployment":
		return deploymentRevisions(ctx, client, namespace, name)
	case "StatefulSet", "DaemonSet":
		return controllerRevisions(ctx, client, kind, namespace, name)
	default:
		return nil, fmt.Errorf("rollout history is not supported for %s", kind)
	}
}

// RolloutUndo rolls a workload back to toRevision (0 = previous revision).
func RolloutUndo(ctx context.Context, client kubernetes.Interface, kind, namespace, name string, toRevision int64) error {
	switch kind {
	case "Deployment":
		return undoDeployment(ctx, client, namespace, name, toRevision)
	case "StatefulSet", "DaemonSet":
		return undoFromControllerRevision(ctx, client, kind, namespace, name, toRevision)
	default:
		return fmt.Errorf("rollout undo is not supported for %s", kind)
	}
}

func deploymentRevisions(ctx context.Context, client kubernetes.Interface, namespace, name string) ([]Revision, error) {
	dep, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	rsList, err := client.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var revs []Revision
	for i := range rsList.Items {
		rs := &rsList.Items[i]
		if !ownedBy(rs.OwnerReferences, dep.UID, "Deployment") {
			continue
		}
		num, _ := strconv.ParseInt(rs.Annotations[revisionAnnotation], 10, 64)
		revs = append(revs, Revision{
			Number: num,
			Cause:  rs.Annotations["kubernetes.io/change-cause"],
			Name:   rs.Name,
		})
	}
	sort.Slice(revs, func(i, j int) bool { return revs[i].Number > revs[j].Number })
	return revs, nil
}

func undoDeployment(ctx context.Context, client kubernetes.Interface, namespace, name string, toRevision int64) error {
	dep, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	rsList, err := client.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	var owned []*appsv1.ReplicaSet
	for i := range rsList.Items {
		rs := &rsList.Items[i]
		if ownedBy(rs.OwnerReferences, dep.UID, "Deployment") {
			owned = append(owned, rs)
		}
	}
	sort.Slice(owned, func(i, j int) bool {
		return revisionNumber(owned[i]) > revisionNumber(owned[j])
	})
	target := pickRollbackRS(owned, toRevision)
	if target == nil {
		return fmt.Errorf("no revision to roll back to")
	}
	template := target.Spec.Template.DeepCopy()
	delete(template.Labels, "pod-template-hash")
	patch, err := json.Marshal(map[string]any{"spec": map[string]any{"template": template}})
	if err != nil {
		return err
	}
	_, err = client.AppsV1().Deployments(namespace).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	return err
}

// pickRollbackRS returns the target RS: the one matching toRevision, or the
// second-newest when toRevision is 0.
func pickRollbackRS(owned []*appsv1.ReplicaSet, toRevision int64) *appsv1.ReplicaSet {
	if toRevision > 0 {
		for _, rs := range owned {
			if revisionNumber(rs) == toRevision {
				return rs
			}
		}
		return nil
	}
	if len(owned) < 2 {
		return nil
	}
	return owned[1] // second-newest = previous
}

func revisionNumber(rs *appsv1.ReplicaSet) int64 {
	n, _ := strconv.ParseInt(rs.Annotations[revisionAnnotation], 10, 64)
	return n
}

func controllerRevisions(ctx context.Context, client kubernetes.Interface, kind, namespace, name string) ([]Revision, error) {
	list, err := client.AppsV1().ControllerRevisions(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	uid, err := workloadUID(ctx, client, kind, namespace, name)
	if err != nil {
		return nil, err
	}
	var revs []Revision
	for i := range list.Items {
		cr := &list.Items[i]
		if !ownedBy(cr.OwnerReferences, uid, kind) {
			continue
		}
		revs = append(revs, Revision{Number: cr.Revision, Name: cr.Name})
	}
	sort.Slice(revs, func(i, j int) bool { return revs[i].Number > revs[j].Number })
	return revs, nil
}

func undoFromControllerRevision(ctx context.Context, client kubernetes.Interface, kind, namespace, name string, toRevision int64) error {
	revs, err := controllerRevisions(ctx, client, kind, namespace, name)
	if err != nil {
		return err
	}
	var targetName string
	switch {
	case toRevision > 0:
		for _, r := range revs {
			if r.Number == toRevision {
				targetName = r.Name
			}
		}
	case len(revs) >= 2:
		targetName = revs[1].Name
	}
	if targetName == "" {
		return fmt.Errorf("no revision to roll back to")
	}
	cr, err := client.AppsV1().ControllerRevisions(namespace).Get(ctx, targetName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if len(cr.Data.Raw) == 0 {
		return fmt.Errorf("controller revision %s has no data", targetName)
	}
	// The ControllerRevision data is a strategic merge patch of the workload.
	switch kind {
	case "StatefulSet":
		_, err = client.AppsV1().StatefulSets(namespace).Patch(ctx, name, types.StrategicMergePatchType, cr.Data.Raw, metav1.PatchOptions{})
	case "DaemonSet":
		_, err = client.AppsV1().DaemonSets(namespace).Patch(ctx, name, types.StrategicMergePatchType, cr.Data.Raw, metav1.PatchOptions{})
	}
	return err
}

func workloadUID(ctx context.Context, client kubernetes.Interface, kind, namespace, name string) (types.UID, error) {
	switch kind {
	case "StatefulSet":
		sts, err := client.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		return sts.UID, nil
	case "DaemonSet":
		ds, err := client.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		return ds.UID, nil
	default:
		return "", fmt.Errorf("unsupported kind %s", kind)
	}
}

func ownedBy(owners []metav1.OwnerReference, uid types.UID, kind string) bool {
	for _, o := range owners {
		if o.Kind == kind && o.UID == uid {
			return true
		}
	}
	return false
}
