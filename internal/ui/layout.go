package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/alphabravo-oss/astrotui/internal/safety"
	"github.com/alphabravo-oss/astrotui/internal/ui/views"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func (a *App) initLayout() {
	a.updates = make(chan uiUpdate, 64)
	a.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() != tcell.KeyF64 {
			return event
		}
		a.updatePending.Store(false)
		count := len(a.updates)
		for i := 0; i < count; i++ {
			update := <-a.updates
			if update.ctx.Err() == nil {
				a.safe(update.run)
			}
		}
		return nil
	})
	a.header = tview.NewTextView()
	a.status = tview.NewTextView()
	a.hints = tview.NewTextView()
	a.table.SetFixed(1, 2).SetBorder(true)
	a.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.header, 3, 0, false).AddItem(a.table, 0, 1, true).
		AddItem(a.status, 1, 0, false).AddItem(a.hints, 2, 0, false)
	a.initBranding()
	a.updateChrome()
}

func (a *App) showMain() {
	a.stopAuxiliary()
	a.updateChrome()
	a.app.SetRoot(a.root, true).SetFocus(a.table)
}

func (a *App) backgroundContext() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

func (a *App) stopBackground() {
	a.stopAuxiliary()
	if a.customCancel != nil {
		a.customCancel()
	}
	if a.aiCancel != nil {
		a.aiCancel()
	}
	if a.options.PortForwards != nil {
		for _, f := range a.options.PortForwards.List() {
			a.options.PortForwards.Kill(f.ID)
		}
	}
	if a.operationCancel != nil {
		a.operationCancel()
	}
	if a.resourceCancel != nil {
		a.resourceCancel()
	}
	if a.cacheCancel != nil {
		a.cacheCancel()
	}
	if a.logCancel != nil {
		a.logCancel()
	}
}

func (a *App) updateChrome() {
	if a.header == nil {
		return
	}
	contextName, namespace, policy := "—", "all", "full"
	if a.options.Client != nil {
		contextName = a.options.Client.Context
		if a.options.Client.Namespace != "" {
			namespace = a.options.Client.Namespace
		}
		policy = safety.PolicyFor(a.options.Config, contextName)
	}
	resource := a.currentGVR.Resource
	if resource == "" {
		resource = "resources"
	}
	a.header.SetText(fmt.Sprintf(" Astro-TUI | %s  %s\n Context: %s\n Namespace: %s", strings.ToUpper(policy), a.options.Version, contextName, namespace))
	a.header.SetTextColor(skinColor(a.skin.Accent, tcell.ColorAqua))
	a.header.SetBackgroundColor(skinColor(a.skin.BG, tcell.ColorBlack))
	a.table.SetTitle(fmt.Sprintf(" %s [%d/%d] ", resource, len(a.visibleObjects), len(a.objects)))
	message := fmt.Sprintf(" %d of %d resources", len(a.visibleObjects), len(a.objects))
	switch {
	case a.watchError != nil:
		message = " Watch error (retrying): " + a.watchError.Error()
	case a.loading:
		message = " Loading " + resource + "…"
	case len(a.objects) == 0:
		message = " No " + resource + " in this scope · :ns change namespace · : resource"
	case len(a.visibleObjects) == 0:
		message = " No matches · Esc clears filter"
	}
	if len(a.marked) > 0 {
		message += fmt.Sprintf(" · %d marked · Space toggle · Ctrl-D delete", len(a.marked))
	}
	if a.filter != "" {
		message += "  /" + a.filter
	}
	if a.customError != nil {
		message += " · config: " + a.customError.Error()
	}
	if a.metricsError != nil {
		message += " · metrics unavailable"
	}
	if a.discoveryError != nil {
		message += " · discovery incomplete (:refresh)"
	}
	a.status.SetText(message).SetTextColor(skinColor(a.skin.Subtle, tcell.ColorGray))
	if a.watchError != nil {
		a.status.SetTextColor(skinColor(a.skin.Error, tcell.ColorRed))
	}
	hints := " Ctrl+P palette  : command  / filter  ? help  q quit\n Enter details  R relationships  E events  b back  0 all namespaces  w wide"
	if a.currentGVR.Resource == "pods" {
		hints += "  l logs  x shell  f forward"
	}
	a.hints.SetText(hints).SetTextColor(skinColor(a.skin.FG, tcell.ColorWhite))
	a.status.SetBackgroundColor(skinColor(a.skin.BG, tcell.ColorBlack))
	a.hints.SetBackgroundColor(skinColor(a.skin.BG, tcell.ColorBlack))
}

func (a *App) displayColumns(renderer views.Renderer) []int {
	headers := renderer.Header()
	var names []string
	_, customized := a.currentCustomView(renderer.GVR())
	if !a.wide && !customized {
		switch renderer.GVR().Resource {
		case "pods":
			names = []string{"NAMESPACE", "NAME", "READY", "STATUS", "RESTARTS", "CPU", "MEM", "AGE", "NODE"}
		case "nodes":
			names = []string{"NAME", "STATUS", "ROLE", "CPU", "MEM", "VERSION", "AGE"}
		case "deployments", "statefulsets", "daemonsets", "replicasets":
			names = []string{"NAMESPACE", "NAME", "READY", "UP-TO-DATE", "AVAILABLE", "AGE"}
		}
	}
	var columns []int
	if len(names) > 0 {
		for _, name := range names {
			for i, column := range headers {
				if column.Name == name {
					columns = append(columns, i)
					break
				}
			}
		}
	} else {
		for i := range headers {
			if !a.wide && headers[i].Priority > 0 {
				continue
			}
			columns = append(columns, i)
		}
	}
	return columns
}

func (a *App) stateColor(state string) tcell.Color {
	switch state {
	case "Running", "Ready", "Active", "Bound", "Succeeded", "Completed":
		return skinColor(a.skin.Accent, tcell.ColorGreen)
	case "Pending", "ContainerCreating", "Terminating", "Unknown":
		return skinColor(a.skin.Warn, tcell.ColorYellow)
	default:
		return skinColor(a.skin.Error, tcell.ColorRed)
	}
}

func sameObject(a, b *unstructured.Unstructured) bool {
	if a == nil || b == nil {
		return false
	}
	if a.GetUID() != "" || b.GetUID() != "" {
		return a.GetUID() == b.GetUID()
	}
	return a.GetNamespace() == b.GetNamespace() && a.GetName() == b.GetName()
}

func (a *App) openSelected() {
	if a.currentGVR.Resource == "namespaces" {
		if obj := a.selectedObject(); obj != nil {
			a.switchNamespace(obj.GetName())
		}
		return
	}
	a.showDetail()
}

func (a *App) showPicker(title string, names []string, selectName func(string)) {
	list := tview.NewList().ShowSecondaryText(false)
	for _, name := range names {
		name := name
		list.AddItem(tview.Escape(name), "", 0, func() { selectName(name) })
	}
	list.SetTitle(" " + title + " · Esc return ").SetBorder(true)
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc {
			a.showMain()
			return nil
		}
		return event
	})
	a.app.SetRoot(list, true)
}

func (a *App) showNamespacePicker() {
	a.showPicker("Namespaces", append([]string{"all"}, a.namespaceNames()...), func(name string) { a.switchNamespace(name); a.showMain() })
}

func (a *App) showContextPicker(ctx context.Context) {
	var names []string
	if a.options.Client != nil {
		for name := range a.options.Client.RawConfig.Contexts {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	a.showPicker("Contexts", names, func(name string) { a.switchContext(ctx, name) })
}

// A bounded dispatch queue avoids tview's synchronous QueueUpdateDraw handshake:
// a producer can exit on cancellation even after the terminal loop has stopped.
// A reserved synthetic key wakes tview, which paints after input capture returns.
type uiUpdate struct {
	ctx context.Context
	run func()
}

func (a *App) queueUpdate(ctx context.Context, run func()) {
	select {
	case <-ctx.Done():
		return
	case a.updates <- uiUpdate{ctx: ctx, run: run}:
	}
	if a.updatePending.CompareAndSwap(false, true) {
		a.app.QueueEvent(tcell.NewEventKey(tcell.KeyF64, 0, tcell.ModNone))
	}
}
