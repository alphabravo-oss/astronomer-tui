package actions

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestPortForwardManager(t *testing.T) {
	var stopped bool
	m := NewPortForwardManager()
	m.Add(PortForward{ID: "one", Local: 8080, Remote: 80, Resource: "pod/api", stop: func() { stopped = true }})
	if len(m.List()) != 1 {
		t.Fatal("expected one forward")
	}
	if !m.Kill("one") || !stopped {
		t.Fatal("expected kill to stop forward")
	}
	forward := NewPortForward(PortForwardSpec{Namespace: "default", Resource: "pod/api", Local: 8080, Remote: 80}, nil)
	if forward.ID == "" || forward.Namespace != "default" || forward.Resource != "pod/api" {
		t.Fatalf("unexpected forward %#v", forward)
	}
}

func TestPortForwardManagerRemoveAndStopAll(t *testing.T) {
	manager := NewPortForwardManager()
	stopped := 0
	manager.Add(PortForward{ID: "one", stop: func() { stopped++ }})
	manager.Add(PortForward{ID: "two", stop: func() { stopped++ }})
	if !manager.Remove("one") || manager.Remove("missing") {
		t.Fatal("unexpected remove result")
	}
	if got := manager.StopAll(); got != 1 || stopped != 1 || len(manager.List()) != 0 {
		t.Fatalf("unexpected stop-all count=%d stopped=%d forwards=%#v", got, stopped, manager.List())
	}
	if manager.StopAll() != 0 {
		t.Fatal("stop-all must be idempotent")
	}
}

func TestDefaultPodForwards(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name:  "app",
			Ports: []corev1.ContainerPort{{ContainerPort: 8080}},
		}}},
	}
	specs := DefaultPodForwards(pod)
	if len(specs) != 1 || specs[0].Resource != "pod/api" || specs[0].Local != 8080 || specs[0].ID() == "" {
		t.Fatalf("unexpected specs %#v", specs)
	}
}

func TestDefaultServiceForwards(t *testing.T) {
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{
			Port:       80,
			TargetPort: intstr.FromInt32(8080),
		}}},
	}
	specs := DefaultServiceForwards(service)
	if len(specs) != 1 || specs[0].Resource != "service/api" || specs[0].Local != 80 || specs[0].Remote != 8080 {
		t.Fatalf("unexpected specs %#v", specs)
	}
}

func TestRunPortForwardRequiresConfigAndPodResource(t *testing.T) {
	err := RunPortForward(context.Background(), nil, nil, PortForwardSpec{Namespace: "default", Resource: "pod/api", Local: 8080, Remote: 8080}, nil, nil)
	if err == nil {
		t.Fatal("expected missing config error")
	}
	err = RunPortForward(context.Background(), &rest.Config{}, nil, PortForwardSpec{Namespace: "default", Resource: "pod/api", Local: 8080, Remote: 8080}, nil, nil)
	if err == nil {
		t.Fatal("expected missing client error")
	}
}

func TestResolveServicePortForwardSpec(t *testing.T) {
	client := kubefake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "api"},
			},
		},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api-b", Namespace: "default", Labels: map[string]string{"app": "api"}}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api-a", Namespace: "default", Labels: map[string]string{"app": "api"}}},
	)
	spec, err := ResolvePortForwardSpec(context.Background(), client, PortForwardSpec{Namespace: "default", Resource: "service/api", Local: 8080, Remote: 8080})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Resource != "pod/api-a" {
		t.Fatalf("expected stable first pod, got %#v", spec)
	}
	_, err = ResolvePortForwardSpec(context.Background(), client, PortForwardSpec{Namespace: "default", Resource: "deployment/api", Local: 8080, Remote: 8080})
	if err == nil {
		t.Fatal("expected unsupported resource error")
	}
	noSelector := kubefake.NewSimpleClientset(&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "headless", Namespace: "default"}})
	if _, err := ResolvePortForwardSpec(context.Background(), noSelector, PortForwardSpec{Namespace: "default", Resource: "service/headless", Local: 8080, Remote: 8080}); err == nil {
		t.Fatal("expected selector error")
	}
}

func TestResolveNamedServiceTargetPort(t *testing.T) {
	client := kubefake.NewSimpleClientset(
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"}, Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "api"}}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api-0", Namespace: "default", Labels: map[string]string{"app": "api"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}}}}}},
	)
	spec, err := ResolvePortForwardSpec(context.Background(), client, PortForwardSpec{Namespace: "default", Resource: "service/api", Local: 80, RemoteName: "http"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Resource != "pod/api-0" || spec.Remote != 8080 || spec.RemoteName != "" {
		t.Fatalf("unexpected resolved spec %#v", spec)
	}
}
