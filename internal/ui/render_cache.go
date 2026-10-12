package ui

import (
	"github.com/alphabravo-oss/astrotui/internal/ui/views"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Informer objects are immutable. Reuse formatted cells while filtering/sorting;
// invalidate the whole snapshot on object, metric, renderer, or scope refresh.
type cachedRenderer struct {
	views.Renderer
	rows map[*unstructured.Unstructured][]views.Cell
}

func (r cachedRenderer) Row(obj *unstructured.Unstructured) []views.Cell {
	if row, ok := r.rows[obj]; ok {
		return row
	}
	row := r.Renderer.Row(obj)
	r.rows[obj] = row
	return row
}
