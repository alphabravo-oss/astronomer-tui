package cluster

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

func TestClusterHealthSummarizesProblems(t *testing.T) {
	replicas := int32(2)
	clientset := kubefake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "ready"}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "down"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"}, Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Name: "app", RestartCount: 3}}}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "waiting", Namespace: "default"}, Status: corev1.PodStatus{Phase: corev1.PodPending}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"}, Spec: appsv1.DeploymentSpec{Replicas: &replicas}, Status: appsv1.DeploymentStatus{ReadyReplicas: 1}},
		&corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: "warning", Namespace: "default"}, Type: corev1.EventTypeWarning},
	)
	snapshot, err := ClusterHealth(context.Background(), &Client{Clientset: clientset}, "default")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.NodesTotal != 2 || snapshot.NodesReady != 1 || snapshot.PodsTotal != 2 || snapshot.PodsPending != 1 || snapshot.ContainerRestarts != 3 || snapshot.WarningEvents != 1 {
		t.Fatalf("unexpected snapshot %#v", snapshot)
	}
	if len(snapshot.Problems) < 3 || snapshot.Problems[0].Severity != "critical" {
		t.Fatalf("expected sorted health problems %#v", snapshot.Problems)
	}
}
