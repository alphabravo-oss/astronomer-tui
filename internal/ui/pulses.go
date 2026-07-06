//revive:disable:exported package-comments
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// pulseTile is one health tile.
type pulseTile struct {
	Label  string
	Value  string
	Color  string // semantic
	Filter string // resource+filter to jump to on Enter
	GVR    schema.GroupVersionResource
}

// pulseTiles aggregates health tiles from cached object lists. Pure for tests.
func pulseTiles(pods, deps, nodes, events []*unstructured.Unstructured, cpuPct, memPct int, metricsOK bool) []pulseTile {
	tiles := []pulseTile{
		countReadyTile("Pods", pods, podRunning, schema.GroupVersionResource{Version: "v1", Resource: "pods"}),
		countReadyTile("Deployments", deps, deploymentReady, schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}),
		countReadyTile("Nodes", nodes, nodeReady, schema.GroupVersionResource{Version: "v1", Resource: "nodes"}),
	}
	warn := 0
	for _, e := range events {
		if t, _, _ := unstructured.NestedString(e.Object, "type"); strings.EqualFold(t, "Warning") {
			warn++
		}
	}
	warnColor := "ok"
	if warn > 0 {
		warnColor = "warn"
	}
	tiles = append(tiles, pulseTile{
		Label: "Warnings", Value: fmt.Sprintf("%d", warn), Color: warnColor,
		GVR: schema.GroupVersionResource{Version: "v1", Resource: "events"}, Filter: "Warning",
	})
	if metricsOK {
		tiles = append(tiles, pulseTile{Label: "Cluster CPU", Value: fmt.Sprintf("%d%%", cpuPct), Color: usageColor(cpuPct)})
		tiles = append(tiles, pulseTile{Label: "Cluster MEM", Value: fmt.Sprintf("%d%%", memPct), Color: usageColor(memPct)})
	}
	return tiles
}

func countReadyTile(label string, objs []*unstructured.Unstructured, ready func(*unstructured.Unstructured) bool, gvr schema.GroupVersionResource) pulseTile {
	total := len(objs)
	ok := 0
	for _, o := range objs {
		if ready(o) {
			ok++
		}
	}
	color := "ok"
	if ok < total {
		color = "warn"
	}
	return pulseTile{Label: label, Value: fmt.Sprintf("%d/%d", ok, total), Color: color, GVR: gvr}
}

func podRunning(u *unstructured.Unstructured) bool {
	phase, _, _ := unstructured.NestedString(u.Object, "status", "phase")
	return phase == "Running" || phase == "Succeeded"
}

func deploymentReady(u *unstructured.Unstructured) bool {
	ready, _, _ := unstructured.NestedInt64(u.Object, "status", "readyReplicas")
	desired, ok, _ := unstructured.NestedInt64(u.Object, "spec", "replicas")
	if !ok {
		return true
	}
	return ready >= desired
}

func nodeReady(u *unstructured.Unstructured) bool {
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if t, _, _ := unstructured.NestedString(m, "type"); t == "Ready" {
			s, _, _ := unstructured.NestedString(m, "status")
			return s == "True"
		}
	}
	return false
}

func usageColor(pct int) string {
	switch {
	case pct >= 90:
		return "error"
	case pct >= 75:
		return "warn"
	default:
		return "ok"
	}
}

// showPulsesView renders a snapshot of cluster health tiles.
func (a *App) showPulsesView(ctx context.Context) {
	if a.cache == nil {
		a.Flash(FlashError, "cache not ready")
		return
	}
	list := func(gvr schema.GroupVersionResource) []*unstructured.Unstructured {
		a.cache.Ensure(gvr)
		syncCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		a.cache.WaitForSync(syncCtx, gvr)
		items, _ := a.cache.List(gvr, "", labels.Everything())
		return items
	}
	pods := list(schema.GroupVersionResource{Version: "v1", Resource: "pods"})
	deps := list(schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"})
	nodes := list(schema.GroupVersionResource{Version: "v1", Resource: "nodes"})
	events := list(schema.GroupVersionResource{Version: "v1", Resource: "events"})

	cpuPct, memPct, metricsOK := a.clusterUsagePct(nodes)
	tiles := pulseTiles(pods, deps, nodes, events, cpuPct, memPct, metricsOK)

	table := tview.NewTable().SetSelectable(true, false)
	table.SetTitle(" pulses ").SetBorder(true)
	for r, tile := range tiles {
		label := tview.NewTableCell(tile.Label + ":").SetSelectable(false)
		value := tview.NewTableCell(tile.Value).SetTextColor(a.semanticColor(tile.Color))
		table.SetCell(r, 0, label)
		table.SetCell(r, 1, value)
	}
	tilesByRow := tiles
	table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch {
		case event.Rune() == 'q':
			a.back()
			return nil
		case event.Key() == tcell.KeyEnter:
			r, _ := table.GetSelection()
			if r >= 0 && r < len(tilesByRow) && tilesByRow[r].GVR.Resource != "" {
				t := tilesByRow[r]
				a.back()
				a.switchResource(ctx, t.GVR)
				if t.Filter != "" {
					a.mu.Lock()
					a.filter = t.Filter
					a.mu.Unlock()
					a.renderCurrent()
				}
			}
			return nil
		}
		return event
	})
	a.pushView(table, table, "pulses", nil)
}

// clusterUsagePct computes cluster CPU/MEM utilization from metrics + node
// allocatable.
func (a *App) clusterUsagePct(nodes []*unstructured.Unstructured) (int, int, bool) {
	a.mu.RLock()
	poller := a.metrics
	a.mu.RUnlock()
	if poller == nil {
		return 0, 0, false
	}
	_, nodeMetrics, _, ok := poller.Snapshot()
	if !ok || len(nodeMetrics) == 0 {
		return 0, 0, false
	}
	alloc := nodeAllocatable(nodes)
	var usedCPU, allocCPU, usedMem, allocMem int64
	for name, nm := range nodeMetrics {
		usedCPU += nm.CPUMilli
		usedMem += nm.MemBytes
		allocCPU += alloc.CPU[name]
		allocMem += alloc.Mem[name]
	}
	cpuPct, memPct := 0, 0
	if allocCPU > 0 {
		cpuPct = int(float64(usedCPU) / float64(allocCPU) * 100)
	}
	if allocMem > 0 {
		memPct = int(float64(usedMem) / float64(allocMem) * 100)
	}
	return cpuPct, memPct, true
}
