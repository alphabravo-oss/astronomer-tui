//revive:disable:exported package-comments
package ui

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/alphabravo-oss/astrotui/internal/config"
	"github.com/alphabravo-oss/astrotui/internal/ui/views"
)

// rowsToCSV renders a header and rendered rows to CSV text.
func rowsToCSV(header []views.Column, rows [][]views.Cell) string {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	head := make([]string, len(header))
	for i, h := range header {
		head[i] = h.Name
	}
	_ = w.Write(head)
	for _, row := range rows {
		rec := make([]string, len(row))
		for i, c := range row {
			rec[i] = c.Text
		}
		_ = w.Write(rec)
	}
	w.Flush()
	return buf.String()
}

// screendumpTable writes the current table (header + visible rows) to a CSV
// file under the config screendumps directory.
func (a *App) screendumpTable() {
	if a.renderer == nil {
		return
	}
	a.mu.RLock()
	objects := append([]*unstructured.Unstructured(nil), a.objects...)
	gvr := a.currentGVR
	filter, sortCol, sortDesc := a.filter, a.sortCol, a.sortDesc
	a.mu.RUnlock()
	rows, _ := buildRows(a.renderer, objects, filter, sortCol, sortDesc, a.rowContext(gvr, objects))
	content := rowsToCSV(a.renderer.Header(), rows)

	dir := filepath.Join(config.DefaultDir(), "screendumps")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		a.Flash(FlashError, "screendump: %s", err.Error())
		return
	}
	name := fmt.Sprintf("%s-%d.csv", gvr.Resource, time.Now().Unix())
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		a.Flash(FlashError, "screendump: %s", err.Error())
		return
	}
	a.Flash(FlashInfo, "wrote %s", path)
}

// showAliasesView lists every resource alias and command keyword.
func (a *App) showAliasesView() {
	table := tview.NewTable().SetSelectable(true, false)
	table.SetTitle(" aliases ").SetBorder(true)
	for col, h := range []string{"ALIAS", "TARGET", "SOURCE"} {
		table.SetCell(0, col, tview.NewTableCell(h).SetTextColor(a.semanticColor("accent")).SetSelectable(false))
	}
	row := 1
	for _, item := range ResourceAliases() {
		target := item.GVR.Resource
		for _, alias := range item.Aliases {
			table.SetCell(row, 0, tview.NewTableCell(alias))
			table.SetCell(row, 1, tview.NewTableCell(target))
			table.SetCell(row, 2, tview.NewTableCell("resource"))
			row++
		}
	}
	for _, cmd := range commandTokens() {
		table.SetCell(row, 0, tview.NewTableCell(":"+cmd))
		table.SetCell(row, 1, tview.NewTableCell(""))
		table.SetCell(row, 2, tview.NewTableCell("command"))
		row++
	}
	table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == 'q' {
			a.back()
			return nil
		}
		return event
	})
	a.pushView(table, table, "aliases", nil)
}
