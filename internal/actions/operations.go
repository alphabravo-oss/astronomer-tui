package actions

import (
	"context"
	"encoding/json"
	"fmt"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"time"
)

// mutate verifies identity and applies an optimistic resourceVersion precondition.
// No automatic retry can accidentally mutate a replacement with the same name.
func (e Executor) mutate(ctx context.Context, r Request, fields map[string]any) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error { return e.mutateOnce(ctx, r, fields) })
}

func (e Executor) mutateOnce(ctx context.Context, r Request, fields map[string]any) error {
	resource := e.Dynamic.Resource(r.GVR)
	var obj *unstructured.Unstructured
	var err error
	if r.Namespace != "" {
		obj, err = resource.Namespace(r.Namespace).Get(ctx, r.ResourceName, metav1.GetOptions{})
	} else {
		obj, err = resource.Get(ctx, r.ResourceName, metav1.GetOptions{})
	}
	if err != nil {
		return err
	}
	if r.UID != "" && obj.GetUID() != r.UID {
		return fmt.Errorf("target was replaced; refresh and select it again")
	}
	fields["metadata"] = map[string]any{"resourceVersion": obj.GetResourceVersion(), "uid": string(obj.GetUID())}
	patch, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	_, err = PatchResource(ctx, e.Dynamic, r.GVR, r.Namespace, r.ResourceName, patch)
	return err
}
func (e Executor) triggerJob(ctx context.Context, r Request) (string, error) {
	cron, err := e.Clientset.BatchV1().CronJobs(r.Namespace).Get(ctx, r.ResourceName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if r.UID != "" && cron.UID != r.UID {
		return "", fmt.Errorf("CronJob was replaced")
	}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{GenerateName: cron.Name + "-manual-", Namespace: cron.Namespace, Labels: cron.Spec.JobTemplate.Labels, Annotations: cron.Spec.JobTemplate.Annotations, OwnerReferences: []metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "CronJob", Name: cron.Name, UID: cron.UID}}}, Spec: *cron.Spec.JobTemplate.Spec.DeepCopy()}
	created, err := e.Clientset.BatchV1().Jobs(r.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return "", err
	}
	return "created job/" + created.Name, nil
}
func (e Executor) drain(ctx context.Context, r Request) error {
	node, err := e.Clientset.CoreV1().Nodes().Get(ctx, r.ResourceName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if r.UID != "" && r.UID != node.UID {
		return fmt.Errorf("node was replaced")
	}
	pods, err := e.Clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + node.Name})
	if err != nil {
		return err
	}
	var evict []corev1.Pod
	for _, pod := range pods.Items {
		if pod.Annotations["kubernetes.io/config.mirror"] != "" {
			continue
		}
		daemon, managed := false, false
		for _, owner := range pod.OwnerReferences {
			if owner.Controller != nil && *owner.Controller {
				managed = true
				if owner.Kind == "DaemonSet" {
					daemon = true
				}
			}
		}
		if daemon {
			continue
		}
		if !managed && pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
			return fmt.Errorf("drain blocked by unmanaged pod %s/%s", pod.Namespace, pod.Name)
		}
		for _, v := range pod.Spec.Volumes {
			if v.EmptyDir != nil {
				return fmt.Errorf("drain blocked by local emptyDir data on %s/%s", pod.Namespace, pod.Name)
			}
		}
		evict = append(evict, pod)
	}
	if err = e.mutate(ctx, r, map[string]any{"spec": map[string]any{"unschedulable": true}}); err != nil {
		return err
	}
	for _, pod := range evict {
		uid := pod.UID
		eviction := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace}, DeleteOptions: &metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}}
		for {
			err = e.Clientset.CoreV1().Pods(pod.Namespace).EvictV1(ctx, eviction)
			if err == nil || apierrors.IsNotFound(err) {
				break
			}
			if !apierrors.IsTooManyRequests(err) {
				return fmt.Errorf("node remains cordoned; eviction %s/%s: %w", pod.Namespace, pod.Name, err)
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("node remains cordoned; waiting for disruption budget: %w", ctx.Err())
			case <-time.After(time.Second):
			}
		}
	}
	for _, pod := range evict {
		for {
			current, err := e.Clientset.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) || err == nil && current.UID != pod.UID {
				break
			}
			if err != nil {
				return fmt.Errorf("node remains cordoned: %w", err)
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("node remains cordoned; waiting for pod termination: %w", ctx.Err())
			case <-time.After(time.Second):
			}
		}
	}
	return nil
}

// NodeUnschedulablePatch is exposed for operator tooling that uses typed clients.
func NodeUnschedulablePatch(uid types.UID, version string, value bool) []byte {
	data, _ := json.Marshal(map[string]any{"metadata": map[string]any{"uid": uid, "resourceVersion": version}, "spec": map[string]any{"unschedulable": value}})
	return data
}
