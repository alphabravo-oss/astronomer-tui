package views

import (
	"encoding/json"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"strings"
)

type TableRenderer struct {
	Renderer
	Table *metav1.Table
	Rows  map[types.UID]metav1.TableRow
}

func NewTableRenderer(base Renderer, t *metav1.Table) TableRenderer {
	r := TableRenderer{Renderer: base, Table: t, Rows: map[types.UID]metav1.TableRow{}}
	for _, row := range t.Rows {
		var meta unstructured.Unstructured
		if json.Unmarshal(row.Object.Raw, &meta) == nil && meta.GetUID() != "" {
			r.Rows[meta.GetUID()] = row
		}
	}
	return r
}
func (r TableRenderer) Header() []Column {
	out := []Column{{Name: "NAMESPACE"}}
	for _, c := range r.Table.ColumnDefinitions {
		out = append(out, Column{Name: strings.ToUpper(c.Name), Priority: c.Priority, Type: c.Type})
	}
	return out
}
func (r TableRenderer) Row(obj *unstructured.Unstructured) []Cell {
	out := []Cell{{Text: obj.GetNamespace()}}
	row, ok := r.Rows[obj.GetUID()]
	if ok {
		var meta unstructured.Unstructured
		_ = json.Unmarshal(row.Object.Raw, &meta)
		ok = meta.GetResourceVersion() == obj.GetResourceVersion()
	}
	for i, c := range r.Table.ColumnDefinitions {
		value := "…"
		if strings.EqualFold(c.Name, "name") {
			value = obj.GetName()
		} else if ok && i < len(row.Cells) {
			if row.Cells[i] == nil {
				value = "<none>"
			} else {
				value = fmt.Sprint(row.Cells[i])
			}
		}
		out = append(out, Cell{Text: value})
	}
	return out
}
