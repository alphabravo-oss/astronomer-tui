//revive:disable:exported package-comments
package ui

import (
	"context"
	"sort"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// showContextsView lists kube contexts as a first-class table; Enter switches.
func (a *App) showContextsView(ctx context.Context) {
	if a.options.Client == nil {
		a.Flash(FlashError, "no kube client")
		return
	}
	raw := a.options.Client.RawConfig
	names := make([]string, 0, len(raw.Contexts))
	for name := range raw.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)

	table := tview.NewTable().SetSelectable(true, false)
	table.SetTitle(" contexts ").SetBorder(true)
	headers := []string{"NAME", "CLUSTER", "AUTHINFO", "NAMESPACE", "ACTIVE"}
	for col, h := range headers {
		table.SetCell(0, col, tview.NewTableCell(h).SetTextColor(a.semanticColor("accent")).SetSelectable(false))
	}
	current := a.options.Client.Context
	for row, name := range names {
		c := raw.Contexts[name]
		cluster, authInfo, ns := "", "", ""
		if c != nil {
			cluster, authInfo, ns = c.Cluster, c.AuthInfo, c.Namespace
		}
		active := ""
		if name == current {
			active = "✓"
		}
		values := []string{name, cluster, authInfo, ns, active}
		color := ""
		if name == current {
			color = "ok"
		}
		for col, v := range values {
			cell := tview.NewTableCell(v)
			if color != "" {
				cell.SetTextColor(a.semanticColor(color))
			}
			table.SetCell(row+1, col, cell)
		}
	}
	table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch {
		case event.Rune() == 'q':
			a.back()
			return nil
		case event.Key() == tcell.KeyEnter:
			r, _ := table.GetSelection()
			idx := r - 1
			if idx >= 0 && idx < len(names) {
				a.switchContext(ctx, names[idx])
			}
			return nil
		}
		return event
	})
	a.pushView(table, table, "contexts", nil)
}
