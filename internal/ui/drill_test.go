package ui

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/alphabravo-oss/astrotui/internal/ui/views"
)

func unstructuredObj(m map[string]any) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: m}
	return obj
}

func TestDrillForWorkloadToPods(t *testing.T) {
	dep := unstructuredObj(map[string]any{
		"kind":     "Deployment",
		"metadata": map[string]any{"name": "api", "namespace": "prod"},
		"spec":     map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"app": "api", "tier": "web"}}},
	})
	target, ok := drillFor(dep, schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"})
	if !ok || target.kind != drillResource || target.gvr.Resource != "pods" {
		t.Fatalf("unexpected target %#v ok=%v", target, ok)
	}
	if target.selector != "app=api,tier=web" {
		t.Fatalf("unexpected selector %q", target.selector)
	}
	if target.namespace != "prod" {
		t.Fatalf("unexpected namespace %q", target.namespace)
	}
}

func TestDrillForServiceUsesPlainSelector(t *testing.T) {
	svc := unstructuredObj(map[string]any{
		"kind":     "Service",
		"metadata": map[string]any{"name": "api", "namespace": "default"},
		"spec":     map[string]any{"selector": map[string]any{"app": "api"}},
	})
	target, ok := drillFor(svc, schema.GroupVersionResource{Version: "v1", Resource: "services"})
	if !ok || target.selector != "app=api" {
		t.Fatalf("unexpected service drill %#v ok=%v", target, ok)
	}
}

func TestDrillForNodeToPodsByNode(t *testing.T) {
	node := unstructuredObj(map[string]any{"kind": "Node", "metadata": map[string]any{"name": "node-a"}})
	target, ok := drillFor(node, schema.GroupVersionResource{Version: "v1", Resource: "nodes"})
	if !ok || target.kind != drillResource || target.nodeName != "node-a" {
		t.Fatalf("unexpected node drill %#v ok=%v", target, ok)
	}
}

func TestDrillForPodToContainers(t *testing.T) {
	pod := unstructuredObj(map[string]any{"kind": "Pod", "metadata": map[string]any{"name": "api-1", "namespace": "default"}})
	target, ok := drillFor(pod, podsGVR)
	if !ok || target.kind != drillContainers {
		t.Fatalf("unexpected pod drill %#v ok=%v", target, ok)
	}
}

func TestDrillForNamespaceSwitches(t *testing.T) {
	ns := unstructuredObj(map[string]any{"kind": "Namespace", "metadata": map[string]any{"name": "kube-system"}})
	target, ok := drillFor(ns, schema.GroupVersionResource{Version: "v1", Resource: "namespaces"})
	if !ok || target.kind != drillNamespace || target.namespace != "kube-system" {
		t.Fatalf("unexpected namespace drill %#v ok=%v", target, ok)
	}
}

func TestDrillForConfigMapFallsBackToDescribe(t *testing.T) {
	cm := unstructuredObj(map[string]any{"kind": "ConfigMap", "metadata": map[string]any{"name": "cfg"}})
	if _, ok := drillFor(cm, schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}); ok {
		t.Fatal("configmap should not drill")
	}
}

func TestContainerRows(t *testing.T) {
	pod := unstructuredObj(map[string]any{
		"kind":     "Pod",
		"metadata": map[string]any{"name": "api", "namespace": "default"},
		"spec": map[string]any{"containers": []any{
			map[string]any{"name": "app", "image": "example/app:1"},
		}},
		"status": map[string]any{"containerStatuses": []any{
			map[string]any{"name": "app", "ready": true, "restartCount": int64(2), "state": map[string]any{"running": map[string]any{}}},
		}},
	})
	metrics := map[string]views.ContainerMetrics{"app": {CPUMilli: 120, MemBytes: 32 * 1024 * 1024}}
	rows := containerRows(pod, metrics)
	if len(rows) != 1 {
		t.Fatalf("expected 1 container row, got %d", len(rows))
	}
	r := rows[0]
	if r[0].text != "app" || r[1].text != "example/app:1" || r[2].text != "true" || r[3].text != "Running" || r[4].text != "2" {
		t.Fatalf("unexpected container row %#v", r)
	}
	if r[5].text != "120m" || r[6].text != "32Mi" {
		t.Fatalf("unexpected container metrics cells %#v", r)
	}
}

func TestFilterByNodeName(t *testing.T) {
	pods := []*unstructured.Unstructured{
		unstructuredObj(map[string]any{"metadata": map[string]any{"name": "a"}, "spec": map[string]any{"nodeName": "node-a"}}),
		unstructuredObj(map[string]any{"metadata": map[string]any{"name": "b"}, "spec": map[string]any{"nodeName": "node-b"}}),
		unstructuredObj(map[string]any{"metadata": map[string]any{"name": "c"}, "spec": map[string]any{"nodeName": "node-a"}}),
	}
	got := filterByNodeName(pods, "node-a")
	if len(got) != 2 || got[0].GetName() != "a" || got[1].GetName() != "c" {
		t.Fatalf("unexpected node filter result %#v", got)
	}
}
