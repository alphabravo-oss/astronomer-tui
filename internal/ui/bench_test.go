package ui

import (
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/alphabravo-oss/astrotui/internal/ui/views"
)

func benchPods(n int) ([]*unstructured.Unstructured, views.RowContext) {
	objs := make([]*unstructured.Unstructured, n)
	pm := map[string]views.PodMetrics{}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("pod-%d", i)
		objs[i] = &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{"name": name, "namespace": "default"},
			"spec":     map[string]any{"containers": []any{map[string]any{"name": "app", "resources": map[string]any{"requests": map[string]any{"cpu": "100m", "memory": "64Mi"}}}}},
			"status":   map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"ready": true}}},
		}}
		objs[i].SetName(name)
		objs[i].SetNamespace("default")
		pm["default/"+name] = views.PodMetrics{CPUMilli: int64(50 + i%100), MemBytes: int64((32 + i%64) * 1024 * 1024)}
	}
	return objs, views.RowContext{MetricsOK: true, PodMetrics: pm}
}

// BenchmarkBuildRowsWithContext measures the metrics-join render path on a
// 1k-pod cluster; must stay well under the 100ms re-render budget.
func BenchmarkBuildRowsWithContext(b *testing.B) {
	renderer := views.Builtins()[schema.GroupVersionResource{Version: "v1", Resource: "pods"}]
	objs, rc := benchPods(1000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, _ := BuildRowsCtx(renderer, objs, "", 0, false, rc)
		if len(rows) != 1000 {
			b.Fatalf("expected 1000 rows, got %d", len(rows))
		}
	}
}
