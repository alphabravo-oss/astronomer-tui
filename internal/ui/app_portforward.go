//revive:disable:exported package-comments
package ui

import (
	"context"
	"fmt"
	"github.com/alphabravo-oss/astrotui/internal/actions"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"io"
	corev1 "k8s.io/api/core/v1"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/cluster"
)

func (a *App) showPortForwardPlan() {
	spec, ok := a.defaultPortForwardSpec()
	if !ok {
		return
	}
	input := tview.NewInputField().
		SetLabel("Port-forward local:remote: ").
		SetText(fmt.Sprintf("%d:%d", spec.Local, spec.Remote)).
		SetFieldWidth(20)
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			updated, err := parsePortForwardSpec(input.GetText(), spec)
			if err != nil {
				a.showMessage("Port-forward unavailable", err.Error())
				return
			}
			a.startPortForwardSpec(updated)
			return
		}
		a.app.SetRoot(a.table, true)
	})
	a.app.SetRoot(input, true)
}

func (a *App) defaultPortForwardSpec() (actions.PortForwardSpec, bool) {
	obj := a.selectedObject()
	if obj == nil {
		return actions.PortForwardSpec{}, false
	}
	var specs []actions.PortForwardSpec
	switch obj.GetKind() {
	case "Pod":
		pod, ok := toTyped[corev1.Pod](obj)
		if ok {
			specs = actions.DefaultPodForwards(pod)
		}
	case "Service":
		service, ok := toTyped[corev1.Service](obj)
		if ok {
			specs = actions.DefaultServiceForwards(service)
		}
	default:
		a.showMessage("Port-forward unavailable", "port-forward is currently implemented for pod and service selections")
		return actions.PortForwardSpec{}, false
	}
	if len(specs) == 0 {
		a.showMessage("Port-forward unavailable", fmt.Sprintf("%s/%s has no declared ports", obj.GetKind(), obj.GetName()))
		return actions.PortForwardSpec{}, false
	}
	return specs[0], true
}

func parsePortForwardSpec(input string, base actions.PortForwardSpec) (actions.PortForwardSpec, error) {
	parts := strings.Split(strings.TrimSpace(input), ":")
	if len(parts) != 2 {
		return actions.PortForwardSpec{}, fmt.Errorf("expected local:remote ports")
	}
	local, err := strconv.Atoi(parts[0])
	if err != nil || local <= 0 {
		return actions.PortForwardSpec{}, fmt.Errorf("local port must be a positive integer")
	}
	remote, err := strconv.Atoi(parts[1])
	if err != nil || remote <= 0 {
		return actions.PortForwardSpec{}, fmt.Errorf("remote port must be a positive integer")
	}
	base.Local = local
	base.Remote = remote
	return base, nil
}

func (a *App) startPortForwardSpec(spec actions.PortForwardSpec) {
	manager := a.options.PortForwards
	if manager == nil {
		manager = actions.NewPortForwardManager()
		a.options.PortForwards = manager
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.RLock()
	client := a.options.Client
	a.mu.RUnlock()
	go a.runPortForward(ctx, client, spec)
	manager.Add(actions.NewPortForward(spec, cancel))
	a.showMessage("Port-forward", fmt.Sprintf("%s %d:%d", spec.Resource, spec.Local, spec.Remote))
}

func (a *App) runPortForward(ctx context.Context, client *cluster.Client, spec actions.PortForwardSpec) {
	if a.options.PortForwardRun != nil {
		_ = a.options.PortForwardRun(ctx, spec)
		return
	}
	if client == nil || client.Config == nil || client.Clientset == nil {
		<-ctx.Done()
		return
	}
	_ = actions.RunPortForward(ctx, client.Config, client.Clientset, spec, io.Discard, io.Discard)
}

func (a *App) showPortForwardManager() {
	manager := a.options.PortForwards
	if manager == nil {
		manager = actions.NewPortForwardManager()
		a.options.PortForwards = manager
	}
	table := tview.NewTable().SetSelectable(true, false)
	table.SetTitle(" Port-forwards ").SetBorder(true)
	renderPortForwardRows(table, manager.List())
	table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch {
		case event.Key() == tcell.KeyEsc || event.Rune() == 'q':
			a.app.SetRoot(a.table, true)
			return nil
		case event.Key() == tcell.KeyCtrlK || event.Rune() == 'k' || event.Rune() == 'd':
			if killSelectedPortForward(table, manager) {
				renderPortForwardRows(table, manager.List())
			}
			return nil
		}
		return event
	})
	a.app.SetRoot(table, true)
}

func renderPortForwardRows(table *tview.Table, forwards []actions.PortForward) {
	table.Clear()
	headers := []string{"ID", "NAMESPACE", "RESOURCE", "LOCAL", "REMOTE", "SINCE"}
	for col, header := range headers {
		table.SetCell(0, col, tview.NewTableCell(header).SetTextColor(tcell.ColorAqua).SetSelectable(false))
	}
	sort.SliceStable(forwards, func(i, j int) bool { return forwards[i].ID < forwards[j].ID })
	for row, forward := range forwards {
		values := []string{
			forward.ID,
			forward.Namespace,
			forward.Resource,
			fmt.Sprint(forward.Local),
			fmt.Sprint(forward.Remote),
			time.Since(forward.Since).Round(time.Second).String(),
		}
		for col, value := range values {
			table.SetCell(row+1, col, tview.NewTableCell(value))
		}
	}
	if len(forwards) == 0 {
		table.SetCell(1, 0, tview.NewTableCell("No active port-forwards"))
	}
}

func portForwardIDAt(table *tview.Table, forwards []actions.PortForward) string {
	row, _ := table.GetSelection()
	index := row - 1
	if index < 0 || index >= len(forwards) {
		return ""
	}
	sort.SliceStable(forwards, func(i, j int) bool { return forwards[i].ID < forwards[j].ID })
	return forwards[index].ID
}

func killSelectedPortForward(table *tview.Table, manager *actions.PortForwardManager) bool {
	if manager == nil {
		return false
	}
	if id := portForwardIDAt(table, manager.List()); id != "" {
		return manager.Kill(id)
	}
	return false
}
