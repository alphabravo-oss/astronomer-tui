package views

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestPodMetricCells(t *testing.T) {
	r := GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, Columns: []string{"NAME", "CPU", "MEM", "%CPU/R", "%MEM/R"}}
	pod := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "api", "namespace": "default"},
		"spec": map[string]any{"containers": []any{
			map[string]any{"name": "app", "resources": map[string]any{"requests": map[string]any{"cpu": "200m", "memory": "128Mi"}}},
		}},
	}}
	pod.SetName("api")
	pod.SetNamespace("default")

	// No metrics -> n/a.
	na := r.RowCtx(pod, RowContext{})
	if na[1].Text != naValue || na[3].Text != naValue {
		t.Fatalf("expected n/a without metrics, got %#v", na)
	}

	rc := RowContext{
		MetricsOK:  true,
		PodMetrics: map[string]PodMetrics{"default/api": {CPUMilli: 100, MemBytes: 64 * 1024 * 1024}},
	}
	row := r.RowCtx(pod, rc)
	if row[1].Text != "100m" {
		t.Fatalf("expected CPU 100m, got %q", row[1].Text)
	}
	if row[2].Text != "64Mi" {
		t.Fatalf("expected MEM 64Mi, got %q", row[2].Text)
	}
	if row[3].Text != "50%" { // 100m / 200m
		t.Fatalf("expected %%CPU/R 50%%, got %q", row[3].Text)
	}
	if row[4].Text != "50%" { // 64Mi / 128Mi
		t.Fatalf("expected %%MEM/R 50%%, got %q", row[4].Text)
	}
}

func TestNodeMetricCellsAndPods(t *testing.T) {
	r := GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "nodes"}, Columns: []string{"NAME", "CPU", "%CPU", "PODS"}}
	node := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "node-a"}}}
	node.SetName("node-a")
	rc := RowContext{
		MetricsOK:   true,
		NodeMetrics: map[string]NodeMetrics{"node-a": {CPUMilli: 500}},
		PodsByNode:  map[string]int{"node-a": 7},
		Alloc:       NodeAllocatable{CPU: map[string]int64{"node-a": 2000}, Pods: map[string]int64{"node-a": 110}},
	}
	row := r.RowCtx(node, rc)
	if row[1].Text != "500m" || row[2].Text != "25%" || row[3].Text != "7/110" {
		t.Fatalf("unexpected node metric row %#v", row)
	}
}

func TestPercentCellColoring(t *testing.T) {
	if c := percentCell(95, 100); c.Color != "error" {
		t.Fatalf("expected error color at 95%%, got %q", c.Color)
	}
	if c := percentCell(80, 100); c.Color != "warn" {
		t.Fatalf("expected warn color at 80%%, got %q", c.Color)
	}
	if c := percentCell(10, 100); c.Color != "" {
		t.Fatalf("expected no color at 10%%, got %q", c.Color)
	}
	if c := percentCell(10, 0); c.Text != naValue {
		t.Fatalf("expected n/a for zero total, got %q", c.Text)
	}
}

func TestCompareCellValuesForQuantities(t *testing.T) {
	// CPU column: 142m < 1 (core), not string order.
	if CompareCellValuesFor("CPU", "142m", "1") >= 0 {
		t.Fatal("expected 142m < 1 core")
	}
	// MEM column: 512Mi < 1.2Gi.
	if CompareCellValuesFor("MEM", "512Mi", "1.2Gi") >= 0 {
		t.Fatal("expected 512Mi < 1.2Gi")
	}
	// Percent column.
	if CompareCellValuesFor("%CPU/R", "9%", "80%") >= 0 {
		t.Fatal("expected 9%% < 80%%")
	}
	// n/a sorts after real values.
	if CompareCellValuesFor("CPU", "100m", naValue) >= 0 {
		t.Fatal("expected 100m before n/a")
	}
	// Age column still uses duration semantics, not quantity.
	if CompareCellValuesFor("AGE", "9m", "3h") >= 0 {
		t.Fatal("expected 9m < 3h for age column")
	}
}
