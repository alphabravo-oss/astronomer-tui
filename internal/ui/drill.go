//revive:disable:exported package-comments
package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/alphabravo-oss/astrotui/internal/actions"
	"github.com/alphabravo-oss/astrotui/internal/logs"
	"github.com/alphabravo-oss/astrotui/internal/ui/views"
)

type drillKind int

const (
	drillNone drillKind = iota
	drillResource
	drillContainers
	drillNamespace
)

// drillTarget describes what Enter should do for the selected object.
type drillTarget struct {
	kind      drillKind
	gvr       schema.GroupVersionResource
	selector  string // label selector string for the drilled resource
	nodeName  string // field filter (node -> pods on node)
	namespace string
	crumb     string
}

var podsGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}

// drillFor decides the contextual navigation for Enter. ok=false means there is
// no meaningful drill and the caller should describe instead.
func drillFor(obj *unstructured.Unstructured, gvr schema.GroupVersionResource) (drillTarget, bool) {
	kind := obj.GetKind()
	if kind == "" {
		kind = fallbackKind(gvr.Resource)
	}
	name := obj.GetName()
	ns := obj.GetNamespace()
	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
		sel := matchLabelsSelector(obj.Object)
		if sel == "" {
			return drillTarget{}, false
		}
		return drillTarget{kind: drillResource, gvr: podsGVR, selector: sel, namespace: ns, crumb: fmt.Sprintf("%s/%s › pods", strings.ToLower(kind), name)}, true
	case "Service":
		sel := plainSelector(obj.Object)
		if sel == "" {
			return drillTarget{}, false
		}
		return drillTarget{kind: drillResource, gvr: podsGVR, selector: sel, namespace: ns, crumb: fmt.Sprintf("svc/%s › pods", name)}, true
	case "CronJob":
		return drillTarget{kind: drillResource, gvr: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, namespace: ns, crumb: fmt.Sprintf("cronjob/%s › jobs", name)}, true
	case "Node":
		return drillTarget{kind: drillResource, gvr: podsGVR, nodeName: name, crumb: fmt.Sprintf("node/%s › pods", name)}, true
	case "Namespace":
		return drillTarget{kind: drillNamespace, namespace: name}, true
	case "Pod":
		return drillTarget{kind: drillContainers, namespace: ns, crumb: fmt.Sprintf("pod/%s › containers", name)}, true
	default:
		return drillTarget{}, false
	}
}

// matchLabelsSelector renders spec.selector.matchLabels as a label selector.
func matchLabelsSelector(obj map[string]any) string {
	m, ok, _ := unstructured.NestedStringMap(obj, "spec", "selector", "matchLabels")
	if !ok || len(m) == 0 {
		return ""
	}
	return joinSelector(m)
}

// plainSelector renders spec.selector (a plain string map, as on Services).
func plainSelector(obj map[string]any) string {
	m, ok, _ := unstructured.NestedStringMap(obj, "spec", "selector")
	if !ok || len(m) == 0 {
		return ""
	}
	return joinSelector(m)
}

func joinSelector(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ",")
}

// drillDown dispatches Enter based on the selected object.
func (a *App) drillDown() {
	obj := a.selectedObject()
	if obj == nil {
		return
	}
	a.mu.RLock()
	gvr := a.currentGVR
	a.mu.RUnlock()
	target, ok := drillFor(obj, gvr)
	if !ok {
		a.showDescribe()
		return
	}
	switch target.kind {
	case drillContainers:
		a.showContainers(obj)
	case drillNamespace:
		a.switchNamespace(target.namespace)
	case drillResource:
		a.drillToResource(target)
	default:
		a.showDescribe()
	}
}

// drillToResource pushes the current table state and switches the root table to
// the drilled resource with its scope applied.
func (a *App) drillToResource(target drillTarget) {
	var selector labels.Selector
	if target.selector != "" {
		parsed, err := labels.Parse(target.selector)
		if err != nil {
			a.Flash(FlashError, "invalid selector %q: %s", target.selector, err.Error())
			return
		}
		selector = parsed
	}
	a.pushTableState(target.crumb)
	// A drilled pod list should span the workload's namespace even if the user
	// was viewing all namespaces.
	if target.namespace != "" && a.options.Client != nil {
		a.options.Client.Namespace = target.namespace
	}
	a.switchResourceScoped(a.runCtx, target.gvr, drillScope{selector: selector, nodeName: target.nodeName, crumb: target.crumb})
	a.updateChrome()
}

// pushTableState records the current root-table view for later restoration.
func (a *App) pushTableState(crumb string) {
	a.mu.Lock()
	st := tableState{
		gvr:      a.currentGVR,
		filter:   a.filter,
		sortCol:  a.sortCol,
		sortDesc: a.sortDesc,
		selector: a.labelSelector,
		nodeName: a.drillNodeName,
		selKey:   a.lastSelKey,
		crumb:    crumb,
	}
	a.tableStack = append(a.tableStack, st)
	a.mu.Unlock()
}

// popTableState restores the most recent saved root-table view. Returns false
// when the drill stack is empty.
func (a *App) popTableState() bool {
	a.mu.Lock()
	if len(a.tableStack) == 0 {
		a.mu.Unlock()
		return false
	}
	st := a.tableStack[len(a.tableStack)-1]
	a.tableStack = a.tableStack[:len(a.tableStack)-1]
	a.mu.Unlock()

	a.switchResourceScoped(a.runCtx, st.gvr, drillScope{selector: st.selector, nodeName: st.nodeName})
	a.mu.Lock()
	a.filter = st.filter
	a.sortCol = st.sortCol
	a.sortDesc = st.sortDesc
	a.lastSelKey = st.selKey
	a.mu.Unlock()
	a.updateChrome()
	return true
}

// filterByNodeName keeps only pods scheduled onto the given node.
func filterByNodeName(items []*unstructured.Unstructured, node string) []*unstructured.Unstructured {
	out := items[:0:0]
	for _, item := range items {
		if n, _, _ := unstructured.NestedString(item.Object, "spec", "nodeName"); n == node {
			out = append(out, item)
		}
	}
	return out
}

// showContainers pushes a per-container view for a pod: name, image, ready,
// state, restarts, plus current CPU/MEM when metrics are available.
func (a *App) showContainers(pod *unstructured.Unstructured) {
	table := tview.NewTable().SetSelectable(true, false)
	table.SetTitle(" containers: " + pod.GetName() + " ").SetBorder(true)
	rows := containerRows(pod, a.containerMetrics(pod))
	headers := []string{"NAME", "IMAGE", "READY", "STATE", "RESTARTS", "CPU", "MEM"}
	for col, h := range headers {
		table.SetCell(0, col, tview.NewTableCell(h).SetTextColor(a.semanticColor("accent")).SetSelectable(false))
	}
	for r, row := range rows {
		for c, cell := range row {
			tc := tview.NewTableCell(cell.text)
			if cell.color != "" {
				tc.SetTextColor(a.semanticColor(cell.color))
			}
			table.SetCell(r+1, c, tc)
		}
	}
	names := containerNames(pod)
	table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch {
		case event.Rune() == 'q':
			a.back()
			return nil
		case event.Rune() == 'l':
			if c := selectedContainerName(table, names); c != "" {
				a.showLogsForContainer(pod, c)
			}
			return nil
		case event.Rune() == 'x':
			if c := selectedContainerName(table, names); c != "" {
				a.execIntoContainer(pod, c)
			}
			return nil
		}
		return event
	})
	a.pushView(table, table, "containers", nil)
}

type containerCell struct {
	text  string
	color string
}

func containerNames(pod *unstructured.Unstructured) []string {
	var names []string
	for _, path := range [][]string{{"spec", "initContainers"}, {"spec", "containers"}} {
		list, ok, _ := unstructured.NestedSlice(pod.Object, path...)
		if !ok {
			continue
		}
		for _, raw := range list {
			if c, ok := raw.(map[string]any); ok {
				if n, ok := c["name"].(string); ok {
					names = append(names, n)
				}
			}
		}
	}
	return names
}

func selectedContainerName(table *tview.Table, names []string) string {
	row, _ := table.GetSelection()
	idx := row - 1
	if idx < 0 || idx >= len(names) {
		return ""
	}
	return names[idx]
}

// containerMetrics returns per-container usage for a pod from the metrics
// snapshot, or nil when metrics are unavailable.
func (a *App) containerMetrics(pod *unstructured.Unstructured) map[string]views.ContainerMetrics {
	a.mu.RLock()
	poller := a.metrics
	a.mu.RUnlock()
	if poller == nil {
		return nil
	}
	pods, _, _, ok := poller.Snapshot()
	if !ok {
		return nil
	}
	pm, has := pods[pod.GetNamespace()+"/"+pod.GetName()]
	if !has {
		return nil
	}
	return pm.Containers
}

// containerRows builds one row per (init and regular) container.
func containerRows(pod *unstructured.Unstructured, metrics map[string]views.ContainerMetrics) [][]containerCell {
	statuses := containerStatusIndex(pod)
	var rows [][]containerCell
	for _, path := range [][]string{{"spec", "initContainers"}, {"spec", "containers"}} {
		list, ok, _ := unstructured.NestedSlice(pod.Object, path...)
		if !ok {
			continue
		}
		for _, raw := range list {
			c, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name, _ := c["name"].(string)
			image, _ := c["image"].(string)
			st := statuses[name]
			cpu, mem := "n/a", "n/a"
			if m, has := metrics[name]; has {
				cpu = formatCPUText(m.CPUMilli)
				mem = formatMemText(m.MemBytes)
			}
			readyColor := "error"
			if st.ready {
				readyColor = "ok"
			}
			rows = append(rows, []containerCell{
				{text: name},
				{text: image},
				{text: fmt.Sprintf("%t", st.ready), color: readyColor},
				{text: st.state, color: stateColor(st.state)},
				{text: fmt.Sprintf("%d", st.restarts)},
				{text: cpu},
				{text: mem},
			})
		}
	}
	return rows
}

type containerStatus struct {
	ready    bool
	state    string
	restarts int64
}

func containerStatusIndex(pod *unstructured.Unstructured) map[string]containerStatus {
	out := map[string]containerStatus{}
	for _, path := range [][]string{{"status", "initContainerStatuses"}, {"status", "containerStatuses"}} {
		list, ok, _ := unstructured.NestedSlice(pod.Object, path...)
		if !ok {
			continue
		}
		for _, raw := range list {
			s, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name, _ := s["name"].(string)
			ready, _, _ := unstructured.NestedBool(s, "ready")
			restarts, _, _ := unstructured.NestedInt64(s, "restartCount")
			out[name] = containerStatus{ready: ready, state: containerStateName(s), restarts: restarts}
		}
	}
	return out
}

func containerStateName(status map[string]any) string {
	if _, ok, _ := unstructured.NestedMap(status, "state", "running"); ok {
		return "Running"
	}
	if reason, ok, _ := unstructured.NestedString(status, "state", "waiting", "reason"); ok {
		return reason
	}
	if reason, ok, _ := unstructured.NestedString(status, "state", "terminated", "reason"); ok {
		return reason
	}
	return "Unknown"
}

func stateColor(state string) string {
	switch state {
	case "Running", "Completed":
		return "ok"
	case "", "Unknown":
		return "muted"
	case "CrashLoopBackOff", "Error", "ImagePullBackOff", "ErrImagePull":
		return "error"
	default:
		return "warn"
	}
}

// formatCPUText/formatMemText mirror the views package formatting for the
// container view (kept local to avoid exporting internals).
func formatCPUText(milli int64) string {
	if milli >= 10000 {
		return fmt.Sprintf("%.1f", float64(milli)/1000)
	}
	return fmt.Sprintf("%dm", milli)
}

func formatMemText(bytes int64) string {
	const mi = 1024 * 1024
	const gi = 1024 * mi
	if bytes >= gi {
		return fmt.Sprintf("%.1fGi", float64(bytes)/float64(gi))
	}
	return fmt.Sprintf("%dMi", bytes/mi)
}

// showLogsForContainer opens the log view for a single pod container.
func (a *App) showLogsForContainer(pod *unstructured.Unstructured, container string) {
	a.showLogsView([]logs.PodLogTarget{{Namespace: pod.GetNamespace(), Pod: pod.GetName(), Container: container}})
}

// execIntoContainer runs an interactive shell in the chosen container.
func (a *App) execIntoContainer(pod *unstructured.Unstructured, container string) {
	typed, ok := toTyped[corev1.Pod](pod)
	if !ok {
		a.Flash(FlashError, "could not decode pod %s", pod.GetName())
		return
	}
	targets, err := actions.ExecTargets(typed, container)
	if err != nil {
		a.showMessage("Exec unavailable", err.Error())
		return
	}
	if !a.canRunExec() {
		var lines []string
		for _, t := range targets {
			lines = append(lines, fmt.Sprintf("kubectl exec -n %s %s -c %s -- %s", t.Namespace, t.Pod, t.Container, strings.Join(t.Command, " ")))
		}
		a.showMessage("Exec", strings.Join(lines, "\n"))
		return
	}
	var runErr error
	run := func() { runErr = runExecFallback(context.Background(), a.execRunner(), targets) }
	if !a.app.Suspend(run) {
		run()
	}
	if runErr != nil {
		a.showMessage("Exec failed", runErr.Error())
		return
	}
	a.showMessage("Exec", "session ended")
}
