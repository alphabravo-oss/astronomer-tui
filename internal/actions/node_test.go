package actions

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func node(name string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func podOn(name, node string, owner string, emptyDir bool) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: node},
	}
	if owner != "" {
		pod.OwnerReferences = []metav1.OwnerReference{{Kind: owner, Name: "owner"}}
	}
	if emptyDir {
		pod.Spec.Volumes = []corev1.Volume{{Name: "scratch", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}
	}
	return pod
}

func TestSetNodeSchedulable(t *testing.T) {
	client := kubefake.NewSimpleClientset(node("node-a"))
	if err := SetNodeSchedulable(context.Background(), client, "node-a", true); err != nil {
		t.Fatal(err)
	}
	got, _ := client.CoreV1().Nodes().Get(context.Background(), "node-a", metav1.GetOptions{})
	if !got.Spec.Unschedulable {
		t.Fatal("expected node cordoned")
	}
	if err := SetNodeSchedulable(context.Background(), client, "node-a", false); err != nil {
		t.Fatal(err)
	}
	got, _ = client.CoreV1().Nodes().Get(context.Background(), "node-a", metav1.GetOptions{})
	if got.Spec.Unschedulable {
		t.Fatal("expected node uncordoned")
	}
}

func TestDrainNodeEvictsAndSkips(t *testing.T) {
	client := kubefake.NewSimpleClientset(
		node("node-a"),
		podOn("app", "node-a", "ReplicaSet", false),
		podOn("agent", "node-a", "DaemonSet", false),
		podOn("scratch", "node-a", "ReplicaSet", true),
	)
	var evicted int
	client.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() == "eviction" {
			evicted++
			return true, nil, nil
		}
		return false, nil, nil
	})

	res, err := DrainNode(context.Background(), client, "node-a", DrainOptions{
		Timeout:            5 * time.Second,
		IgnoreDaemonSets:   true,
		DeleteEmptyDirData: false,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// app evicted; agent skipped (DS); scratch skipped (emptyDir).
	if res.Evicted != 1 {
		t.Fatalf("expected 1 eviction, got %d (%d actual)", res.Evicted, evicted)
	}
	if res.Skipped != 2 {
		t.Fatalf("expected 2 skipped, got %d", res.Skipped)
	}
	// Node should be cordoned as part of drain.
	n, _ := client.CoreV1().Nodes().Get(context.Background(), "node-a", metav1.GetOptions{})
	if !n.Spec.Unschedulable {
		t.Fatal("expected node cordoned during drain")
	}
}

func TestShouldSkipPodRules(t *testing.T) {
	mirror := podOn("m", "n", "", false)
	mirror.Annotations = map[string]string{"kubernetes.io/config.mirror": "x"}
	if skip, _ := shouldSkipPod(mirror, DrainOptions{}); !skip {
		t.Fatal("expected mirror pod skipped")
	}
	ds := podOn("d", "n", "DaemonSet", false)
	if skip, _ := shouldSkipPod(ds, DrainOptions{IgnoreDaemonSets: true}); !skip {
		t.Fatal("expected DS pod skipped when ignoring DS")
	}
	if skip, _ := shouldSkipPod(ds, DrainOptions{IgnoreDaemonSets: false}); skip {
		t.Fatal("expected DS pod evicted when not ignoring DS")
	}
	ed := podOn("e", "n", "ReplicaSet", true)
	if skip, _ := shouldSkipPod(ed, DrainOptions{DeleteEmptyDirData: false}); !skip {
		t.Fatal("expected emptyDir pod skipped by default")
	}
	if skip, _ := shouldSkipPod(ed, DrainOptions{DeleteEmptyDirData: true}); skip {
		t.Fatal("expected emptyDir pod evicted when override set")
	}
}
