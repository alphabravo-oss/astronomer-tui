package views

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ctxRenderer is a test ContextRenderer that colors a cell using RowContext.
type ctxRenderer struct{ GenericRenderer }

func (c ctxRenderer) RowCtx(u *unstructured.Unstructured, rc RowContext) []Cell {
	cell := Cell{Text: u.GetName()}
	if m, ok := rc.PodMetrics[u.GetNamespace()+"/"+u.GetName()]; ok {
		cell.Color = "ok"
		cell.Text = u.GetName()
		_ = m
	}
	return []Cell{cell}
}

func TestRenderRowUsesContextRendererWhenAvailable(t *testing.T) {
	r := ctxRenderer{GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, Columns: []string{"NAME"}}}
	obj := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "api", "namespace": "default"}}}
	obj.SetName("api")
	obj.SetNamespace("default")

	// Without matching metrics -> no color.
	plain := RenderRow(r, obj, RowContext{})
	if plain[0].Color != "" {
		t.Fatalf("expected no color without metrics, got %q", plain[0].Color)
	}

	// With metrics -> colored.
	rc := RowContext{PodMetrics: map[string]PodMetrics{"default/api": {CPUMilli: 100}}, MetricsOK: true}
	colored := RenderRow(r, obj, rc)
	if colored[0].Color != "ok" {
		t.Fatalf("expected ok color, got %q", colored[0].Color)
	}
}

func TestRenderRowFallsBackToRowForPlainRenderer(t *testing.T) {
	r := GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, Columns: []string{"NAME"}}
	obj := &unstructured.Unstructured{}
	obj.SetName("api")
	row := RenderRow(r, obj, RowContext{})
	if len(row) != 1 || row[0].Text != "api" {
		t.Fatalf("unexpected fallback row %#v", row)
	}
}
