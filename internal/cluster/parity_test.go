package cluster

import (
	"context"
	"errors"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestRBACScopeIdentityAndReverseLookup(t *testing.T) {
	role := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "read"}, Rules: []rbacv1.PolicyRule{{Verbs: []string{"get", "list"}, APIGroups: []string{""}, Resources: []string{"pods"}}, {Verbs: []string{"get"}, APIGroups: []string{""}, Resources: []string{"pods/log"}, ResourceNames: []string{"one"}}}}
	binding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "team", Namespace: "one"}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "read"}, Subjects: []rbacv1.Subject{{Kind: "Group", Name: "team"}, {Kind: "ServiceAccount", Name: "runner", Namespace: "one"}}}
	s := ReadRBAC(context.Background(), fake.NewSimpleClientset(role, binding), "one")
	if len(s.Warnings) != 0 || len(s.Grants) != 4 {
		t.Fatalf("%+v", s)
	}
	if len(s.WhoCan("get", "", "pods", "one", "")) != 2 || len(s.WhoCan("get", "", "pods", "two", "")) != 0 || len(s.WhoCan("get", "", "pods", "", "")) != 0 {
		t.Fatal("RoleBinding escaped namespace")
	}
	if len(s.WhoCan("get", "", "pods/log", "one", "one")) != 2 || len(s.WhoCan("get", "", "pods/log", "one", "")) != 0 {
		t.Fatal("resourceNames/subresource mismatch")
	}
	if len(s.ForSubject(rbacv1.Subject{Kind: "User", Name: "alice"}, []string{"team"})) != 2 {
		t.Fatal("missing group grants")
	}
	if len(s.ForSubject(rbacv1.Subject{Kind: "ServiceAccount", Namespace: "two", Name: "runner"}, nil)) != 0 {
		t.Fatal("cross-namespace SA identity")
	}
}
func TestRBACPartialVisibility(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("list", "clusterroles", func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("forbidden") })
	s := ReadRBAC(context.Background(), client, "default")
	if len(s.Warnings) != 1 {
		t.Fatal("permission failure rendered as empty success")
	}
}
func TestHealthReadinessAndObservedGeneration(t *testing.T) {
	pod := &unstructured.Unstructured{Object: map[string]any{"kind": "Pod", "status": map[string]any{"phase": "Running"}}}
	if ResourceHealth(pod) != "warning" {
		t.Fatal("Running alone is not Ready")
	}
	pod.Object["status"].(map[string]any)["conditions"] = []any{map[string]any{"type": "Ready", "status": "True"}}
	if ResourceHealth(pod) != "healthy" {
		t.Fatal("ready pod classified unhealthy")
	}
	d := &unstructured.Unstructured{Object: map[string]any{"kind": "Deployment", "metadata": map[string]any{"generation": int64(2)}, "spec": map[string]any{"replicas": int64(2)}, "status": map[string]any{"observedGeneration": int64(1), "readyReplicas": int64(2)}}}
	if ResourceHealth(d) != "unknown" {
		t.Fatal("stale controller status treated as healthy")
	}
	_ = unstructured.SetNestedField(d.Object, int64(2), "status", "observedGeneration")
	if ResourceHealth(d) != "healthy" {
		t.Fatal("ready rollout not healthy")
	}
}

func TestHealthAndGraphPartialNamespaceSnapshot(t *testing.T) {
	lists := map[schema.GroupVersionResource]string{}
	for _, g := range HealthGroups {
		lists[g.GVR] = "List"
	}
	lists[schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}] = "EndpointSliceList"
	pod := func(ns, name string) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": name, "namespace": ns}, "status": map[string]any{"phase": "Succeeded"}}}
	}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, pod("one", "a"), pod("two", "hidden"))
	client.PrependReactor("list", "nodes", func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("forbidden") })
	counts := ReadHealth(context.Background(), client, "one")
	if counts[0].Err == nil {
		t.Fatal("forbidden nodes shown as zero healthy")
	}
	if counts[1].Total != 1 || counts[1].Healthy != 1 {
		t.Fatalf("namespace health: %+v", counts[1])
	}
	graph := ReadGraph(context.Background(), client, schema.GroupVersionResource{Version: "v1", Resource: "nodes"}, "one")
	if len(graph.Warnings) != 1 || len(graph.Objects) != 1 || graph.Objects[0].Object.GetName() != "a" {
		t.Fatalf("partial graph: %+v", graph)
	}
}
