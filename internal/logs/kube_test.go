package logs

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

func TestContainerSelectionAndTargets(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{Name: "init"}},
			Containers:     []corev1.Container{{Name: "app"}, {Name: "sidecar"}},
		},
	}
	names := ContainerNames(pod)
	if len(names) != 3 || names[0] != "app" || names[2] != "init" {
		t.Fatalf("unexpected names %#v", names)
	}
	if hint := ContainerPickerHint(pod); hint == "" {
		t.Fatal("expected picker hint for multi-container pod")
	}
	targets := TargetsForPods([]corev1.Pod{pod})
	if len(targets) != 1 || targets[0].Container != "app" {
		t.Fatalf("unexpected targets %#v", targets)
	}
	allTargets := TargetsForPodsAllContainers([]corev1.Pod{pod})
	if len(allTargets) != 3 || allTargets[0].Container != "app" || allTargets[2].Container != "init" {
		t.Fatalf("unexpected all-container targets %#v", allTargets)
	}
}

func TestPodsForSelector(t *testing.T) {
	client := kubefake.NewSimpleClientset(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default", Labels: map[string]string{"app": "api"}}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "default", Labels: map[string]string{"app": "worker"}}},
	)
	pods, err := PodsForSelector(context.Background(), client, "default", labels.SelectorFromSet(labels.Set{"app": "api"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 1 || pods[0].Name != "api" {
		t.Fatalf("unexpected pods %#v", pods)
	}
}
