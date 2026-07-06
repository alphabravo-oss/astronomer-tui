package actions

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

func deploymentWithUID(name, uid string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID(uid)},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:3"}}}},
		},
	}
}

func rsRevision(name, owner, revision, image string) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "default",
			Annotations: map[string]string{revisionAnnotation: revision, "kubernetes.io/change-cause": "deploy " + image},
			OwnerReferences: []metav1.OwnerReference{{
				Kind: "Deployment", Name: "api", UID: types.UID(owner),
			}},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"pod-template-hash": "abc", "app": "api"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: image}}},
			},
		},
	}
}

func TestRolloutRevisionsSortedNewestFirst(t *testing.T) {
	client := kubefake.NewSimpleClientset(
		deploymentWithUID("api", "uid-1"),
		rsRevision("api-1", "uid-1", "1", "app:1"),
		rsRevision("api-3", "uid-1", "3", "app:3"),
		rsRevision("api-2", "uid-1", "2", "app:2"),
		rsRevision("other-1", "uid-9", "1", "other:1"), // different owner, ignored
	)
	revs, err := RolloutRevisions(context.Background(), client, "Deployment", "default", "api")
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 3 {
		t.Fatalf("expected 3 revisions, got %d", len(revs))
	}
	if revs[0].Number != 3 || revs[2].Number != 1 {
		t.Fatalf("expected newest-first order, got %#v", revs)
	}
}

func TestRolloutUndoDeploymentToPreviousRevision(t *testing.T) {
	client := kubefake.NewSimpleClientset(
		deploymentWithUID("api", "uid-1"),
		rsRevision("api-1", "uid-1", "1", "app:1"),
		rsRevision("api-2", "uid-1", "2", "app:2"),
		rsRevision("api-3", "uid-1", "3", "app:3"),
	)
	// Undo with 0 => roll back to previous (revision 2, image app:2).
	if err := RolloutUndo(context.Background(), client, "Deployment", "default", "api", 0); err != nil {
		t.Fatal(err)
	}
	dep, _ := client.AppsV1().Deployments("default").Get(context.Background(), "api", metav1.GetOptions{})
	if got := dep.Spec.Template.Spec.Containers[0].Image; got != "app:2" {
		t.Fatalf("expected rollback to app:2, got %s", got)
	}
	if _, ok := dep.Spec.Template.Labels["pod-template-hash"]; ok {
		t.Fatal("pod-template-hash should be stripped from rolled-back template")
	}
}

func TestRolloutUndoToSpecificRevision(t *testing.T) {
	client := kubefake.NewSimpleClientset(
		deploymentWithUID("api", "uid-1"),
		rsRevision("api-1", "uid-1", "1", "app:1"),
		rsRevision("api-3", "uid-1", "3", "app:3"),
	)
	if err := RolloutUndo(context.Background(), client, "Deployment", "default", "api", 1); err != nil {
		t.Fatal(err)
	}
	dep, _ := client.AppsV1().Deployments("default").Get(context.Background(), "api", metav1.GetOptions{})
	if got := dep.Spec.Template.Spec.Containers[0].Image; got != "app:1" {
		t.Fatalf("expected rollback to app:1, got %s", got)
	}
}
