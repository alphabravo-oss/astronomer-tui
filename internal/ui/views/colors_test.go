package views

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestStateColorPods(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	cases := []struct {
		name   string
		obj    map[string]any
		expect string
	}{
		{"crashloop", map[string]any{
			"spec":   map[string]any{"containers": []any{map[string]any{"name": "app"}}},
			"status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"state": map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff"}}}}},
		}, "error"},
		{"pending", map[string]any{
			"spec":   map[string]any{"containers": []any{map[string]any{"name": "app"}}},
			"status": map[string]any{"phase": "Pending"},
		}, "warn"},
		{"running-ready", map[string]any{
			"spec":   map[string]any{"containers": []any{map[string]any{"name": "app"}}},
			"status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"ready": true}}},
		}, ""},
		{"running-notready", map[string]any{
			"spec":   map[string]any{"containers": []any{map[string]any{"name": "app"}}},
			"status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"ready": false}}},
		}, "warn"},
		{"terminating", map[string]any{
			"metadata": map[string]any{"deletionTimestamp": "2026-01-01T00:00:00Z"},
			"spec":     map[string]any{"containers": []any{map[string]any{"name": "app"}}},
			"status":   map[string]any{"phase": "Running"},
		}, "muted"},
	}
	for _, tc := range cases {
		u := &unstructured.Unstructured{Object: tc.obj}
		if got := StateColor(gvr, u); got != tc.expect {
			t.Fatalf("%s: got %q want %q", tc.name, got, tc.expect)
		}
	}
}

func TestStateColorNodesAndEvents(t *testing.T) {
	nodeGVR := schema.GroupVersionResource{Version: "v1", Resource: "nodes"}
	notReady := &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{"nodeInfo": map[string]any{}, "conditions": []any{map[string]any{"type": "Ready", "status": "False"}}},
	}}
	if got := StateColor(nodeGVR, notReady); got != "error" {
		t.Fatalf("expected notready node error, got %q", got)
	}
	cordoned := &unstructured.Unstructured{Object: map[string]any{
		"spec":   map[string]any{"unschedulable": true},
		"status": map[string]any{"nodeInfo": map[string]any{}, "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
	}}
	if got := StateColor(nodeGVR, cordoned); got != "warn" {
		t.Fatalf("expected cordoned node warn, got %q", got)
	}

	eventGVR := schema.GroupVersionResource{Version: "v1", Resource: "events"}
	warnEvent := &unstructured.Unstructured{Object: map[string]any{"type": "Warning"}}
	if got := StateColor(eventGVR, warnEvent); got != "warn" {
		t.Fatalf("expected warning event warn, got %q", got)
	}
}

func TestStateColorWorkloadUnderReplicated(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	under := &unstructured.Unstructured{Object: map[string]any{
		"spec":   map[string]any{"replicas": int64(3)},
		"status": map[string]any{"readyReplicas": int64(1)},
	}}
	if got := StateColor(gvr, under); got != "warn" {
		t.Fatalf("expected under-replicated warn, got %q", got)
	}
	full := &unstructured.Unstructured{Object: map[string]any{
		"spec":   map[string]any{"replicas": int64(3)},
		"status": map[string]any{"readyReplicas": int64(3)},
	}}
	if got := StateColor(gvr, full); got != "" {
		t.Fatalf("expected fully-ready no color, got %q", got)
	}
}
