package actions

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

func TestExecTargetsUseShellFallback(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}, {Name: "sidecar"}}},
	}
	if !NeedsContainerPicker(pod) {
		t.Fatal("expected picker for multi-container pod")
	}
	targets, err := ExecTargets(pod, "sidecar")
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 3 || targets[0].Command[0] != "bash" || targets[2].Command[0] != "/bin/ash" {
		t.Fatalf("unexpected targets %#v", targets)
	}
	if targets[0].Container != "sidecar" {
		t.Fatalf("expected preferred container, got %s", targets[0].Container)
	}
}

func TestRunExecRequiresConfigAndClient(t *testing.T) {
	err := RunExec(context.Background(), nil, nil, ExecTarget{Namespace: "default", Pod: "api", Container: "app", Command: []string{"sh"}}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected missing config error")
	}
	err = RunExec(context.Background(), &rest.Config{}, nil, ExecTarget{Namespace: "default", Pod: "api", Container: "app", Command: []string{"sh"}}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected missing client error")
	}
	_, err = ExecTargets(corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "empty", Namespace: "default"}}, "")
	if err == nil {
		t.Fatal("expected no containers error")
	}
}
