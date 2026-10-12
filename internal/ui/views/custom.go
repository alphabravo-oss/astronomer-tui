package views

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

type CustomRenderer struct {
	Renderer
	Columns []Column
	Paths   []string
}

func (r CustomRenderer) Header() []Column { return r.Columns }
func (r CustomRenderer) Row(u *unstructured.Unstructured) []Cell {
	base := r.Renderer.Row(u)
	headers := r.Renderer.Header()
	out := make([]Cell, len(r.Columns))
	for i, col := range r.Columns {
		if r.Paths[i] != "" {
			out[i] = Cell{Text: jsonPathValue(u.Object, r.Paths[i])}
			continue
		}
		for j, c := range headers {
			if c.Name == col.Name {
				out[i] = base[j]
				break
			}
		}
	}
	return out
}
