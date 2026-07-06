//revive:disable:exported package-comments
package views

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// PodMetrics holds resolved current usage for a pod and its containers, in
// canonical units (CPU milli-cores, memory bytes) so renderers only format.
type PodMetrics struct {
	CPUMilli   int64
	MemBytes   int64
	Containers map[string]ContainerMetrics
}

// ContainerMetrics is per-container current usage.
type ContainerMetrics struct {
	CPUMilli int64
	MemBytes int64
}

// NodeMetrics holds resolved current usage for a node.
type NodeMetrics struct {
	CPUMilli int64
	MemBytes int64
}

// NodeAllocatable holds per-node allocatable capacity used for % columns and
// header gauges. Keyed by node name.
type NodeAllocatable struct {
	CPU  map[string]int64 // milli-cores
	Mem  map[string]int64 // bytes
	Pods map[string]int64 // pod count
}

// RowContext carries cross-object data that a single object cannot provide on
// its own: metrics joins, relationship counts, and a stable clock for tests.
// It is always safe to pass the zero value — renderers must degrade to "n/a"
// or empty when a map lookup misses.
type RowContext struct {
	PodMetrics  map[string]PodMetrics  // key: "namespace/name"
	NodeMetrics map[string]NodeMetrics // key: node name
	PodsByNode  map[string]int         // key: node name -> running pod count
	MetricsOK   bool                   // false => metrics unavailable, render n/a
	Alloc       NodeAllocatable        // node allocatable capacity
}

// ContextRenderer is an optional renderer capability: it renders a row using
// cross-object context. Renderers that don't need context implement only
// Renderer and BuildRows falls back to Row.
type ContextRenderer interface {
	Renderer
	RowCtx(*unstructured.Unstructured, RowContext) []Cell
}

// RenderRow renders one object with the richest method the renderer supports.
func RenderRow(r Renderer, u *unstructured.Unstructured, rc RowContext) []Cell {
	if cr, ok := r.(ContextRenderer); ok {
		return cr.RowCtx(u, rc)
	}
	return r.Row(u)
}
