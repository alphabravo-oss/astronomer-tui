package actions

import (
	"context"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"os"
	"testing"
	"time"
)

func TestNodeShellIdentityAndCleanup(t *testing.T) {
	for _, mode := range []string{"running", "cancelled", "replacement", "exited"} {
		t.Run(mode, func(t *testing.T) {
			c := fake.NewSimpleClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node", UID: "node-uid"}})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			deleted := false
			c.PrependReactor("create", "pods", func(a ktesting.Action) (bool, runtime.Object, error) {
				p := a.(ktesting.CreateAction).GetObject().(*corev1.Pod).DeepCopy()
				if !p.Spec.HostPID || !*p.Spec.Containers[0].SecurityContext.Privileged || p.Spec.NodeName != "node" || *p.Spec.ActiveDeadlineSeconds != 3600 {
					t.Fatal("helper confinement or privileges changed")
				}
				p.Name = "helper"
				p.UID = "helper-uid"
				if mode == "cancelled" {
					cancel()
				}
				return true, p, nil
			})
			c.PrependReactor("get", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
				p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "helper", UID: "helper-uid"}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "shell", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}}
				if mode == "replacement" {
					p.UID = "different"
				}
				if mode == "exited" {
					p.Status.Phase = corev1.PodFailed
				}
				return true, p, nil
			})
			c.PrependReactor("delete", "pods", func(a ktesting.Action) (bool, runtime.Object, error) {
				opts := a.(ktesting.DeleteAction).GetDeleteOptions()
				if opts.Preconditions == nil || *opts.Preconditions.UID != "helper-uid" || opts.GracePeriodSeconds == nil || *opts.GracePeriodSeconds != 0 {
					t.Fatal("cleanup did not preserve identity")
				}
				deleted = true
				return true, nil, nil
			})
			target, cleanup, err := PrepareNodeShell(ctx, c, "node", "node-uid", "", "")
			if mode == "running" {
				if err != nil || target.UID != "helper-uid" {
					t.Fatalf("target %v: %v", target, err)
				}
				if err = cleanup(); err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("invalid helper accepted")
			}
			if !deleted {
				t.Fatal("helper not cleaned up")
			}
		})
	}
	c := fake.NewSimpleClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node", UID: "new"}})
	for _, uid := range []string{"", "old"} {
		if _, _, err := PrepareNodeShell(context.Background(), c, "node", types.UID(uid), "", ""); err == nil {
			t.Fatal("missing/stale identity accepted")
		}
	}
}

func TestResizeQueueStopsOnCancellation(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "nonterminal")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx, cancel := context.WithCancel(context.Background())
	q := resizeQueue(ctx, f)
	cancel()
	done := make(chan bool, 1)
	go func() { done <- q.Next() == nil }()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("cancelled queue returned size")
		}
	case <-time.After(time.Second):
		t.Fatal("resize worker leaked")
	}
}

func TestExplicitExecContainerMustExist(t *testing.T) {
	p := corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main"}}, InitContainers: []corev1.Container{{Name: "init"}}, EphemeralContainers: []corev1.EphemeralContainer{{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "debug"}}}}}
	for _, name := range []string{"init", "debug"} {
		if got, ok := pickContainer(p, name); !ok || got != name {
			t.Fatalf("%s: %s %v", name, got, ok)
		}
	}
	if _, ok := pickContainer(p, "absent"); ok {
		t.Fatal("missing container silently selected")
	}
}
