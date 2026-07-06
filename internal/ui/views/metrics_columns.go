//revive:disable:exported package-comments
package views

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const naValue = "n/a"

// metricColumns are the header names that come from the metrics poller / row
// relationships rather than from the object itself.
func isMetricColumn(col string) bool {
	switch strings.ToUpper(col) {
	case "CPU", "MEM", "%CPU", "%MEM", "%CPU/R", "%CPU/L", "%MEM/R", "%MEM/L", "PODS":
		return true
	default:
		return false
	}
}

// metricCell computes a metrics/relationship column for pods and nodes. The
// bool is false when the column isn't a metric column for this resource, so the
// caller falls back to normal field extraction.
func (g GenericRenderer) metricCell(u *unstructured.Unstructured, col string, rc RowContext) (Cell, bool) {
	up := strings.ToUpper(col)
	if !isMetricColumn(up) {
		return Cell{}, false
	}
	switch g.Resource.Resource {
	case "pods":
		return podMetricCell(u, up, rc), true
	case "nodes":
		return nodeMetricCell(u, up, rc), true
	default:
		return Cell{Text: naValue, Color: "muted"}, true
	}
}

func podMetricCell(u *unstructured.Unstructured, col string, rc RowContext) Cell {
	pm, has := rc.PodMetrics[u.GetNamespace()+"/"+u.GetName()]
	if !rc.MetricsOK || !has {
		return Cell{Text: naValue, Color: "muted"}
	}
	req, lim := podResourceTotals(u.Object)
	switch col {
	case "CPU":
		return Cell{Text: formatCPU(pm.CPUMilli)}
	case "MEM":
		return Cell{Text: formatMem(pm.MemBytes)}
	case "%CPU/R":
		return percentCell(pm.CPUMilli, req.cpuMilli)
	case "%CPU/L":
		return percentCell(pm.CPUMilli, lim.cpuMilli)
	case "%MEM/R":
		return percentCell(pm.MemBytes, req.memBytes)
	case "%MEM/L":
		return percentCell(pm.MemBytes, lim.memBytes)
	}
	return Cell{Text: naValue, Color: "muted"}
}

func nodeMetricCell(u *unstructured.Unstructured, col string, rc RowContext) Cell {
	name := u.GetName()
	if col == "PODS" {
		count, ok := rc.PodsByNode[name]
		if !ok {
			return Cell{Text: naValue, Color: "muted"}
		}
		if alloc := rc.Alloc.Pods[name]; alloc > 0 {
			return Cell{Text: fmt.Sprintf("%d/%d", count, alloc)}
		}
		return Cell{Text: fmt.Sprintf("%d", count)}
	}
	nm, has := rc.NodeMetrics[name]
	if !rc.MetricsOK || !has {
		return Cell{Text: naValue, Color: "muted"}
	}
	switch col {
	case "CPU":
		return Cell{Text: formatCPU(nm.CPUMilli)}
	case "MEM":
		return Cell{Text: formatMem(nm.MemBytes)}
	case "%CPU":
		return percentCell(nm.CPUMilli, rc.Alloc.CPU[name])
	case "%MEM":
		return percentCell(nm.MemBytes, rc.Alloc.Mem[name])
	}
	return Cell{Text: naValue, Color: "muted"}
}

type resourceTotals struct {
	cpuMilli int64
	memBytes int64
}

// podResourceTotals sums container resource requests and limits from a pod
// spec (including init containers via max semantics is skipped for simplicity;
// regular containers dominate the common case).
func podResourceTotals(obj map[string]any) (requests, limits resourceTotals) {
	containers, ok, _ := unstructured.NestedSlice(obj, "spec", "containers")
	if !ok {
		return
	}
	for _, raw := range containers {
		c, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		requests.cpuMilli += quantityMilliValue(c, "resources", "requests", "cpu")
		requests.memBytes += quantityByteValue(c, "resources", "requests", "memory")
		limits.cpuMilli += quantityMilliValue(c, "resources", "limits", "cpu")
		limits.memBytes += quantityByteValue(c, "resources", "limits", "memory")
	}
	return
}

func quantityMilliValue(obj map[string]any, path ...string) int64 {
	v, ok, _ := unstructured.NestedString(obj, path...)
	if !ok || v == "" {
		return 0
	}
	q, err := resource.ParseQuantity(v)
	if err != nil {
		return 0
	}
	return q.MilliValue()
}

func quantityByteValue(obj map[string]any, path ...string) int64 {
	v, ok, _ := unstructured.NestedString(obj, path...)
	if !ok || v == "" {
		return 0
	}
	q, err := resource.ParseQuantity(v)
	if err != nil {
		return 0
	}
	return q.Value()
}

// formatCPU renders milli-cores as "142m" (or "1.5" cores above 10000m).
func formatCPU(milli int64) string {
	if milli >= 10000 {
		return fmt.Sprintf("%.1f", float64(milli)/1000)
	}
	return fmt.Sprintf("%dm", milli)
}

// formatMem renders bytes as Mi, switching to Gi at 1024Mi.
func formatMem(bytes int64) string {
	const mi = 1024 * 1024
	const gi = 1024 * mi
	if bytes >= gi {
		return fmt.Sprintf("%.1fGi", float64(bytes)/float64(gi))
	}
	return fmt.Sprintf("%dMi", bytes/mi)
}

// percentCell renders used/total as a percentage, coloring hot values, or n/a
// when the denominator is unknown (no request/limit/allocatable set).
func percentCell(used, total int64) Cell {
	if total <= 0 {
		return Cell{Text: naValue, Color: "muted"}
	}
	pct := int(float64(used) / float64(total) * 100)
	cell := Cell{Text: fmt.Sprintf("%d%%", pct)}
	switch {
	case pct >= 90:
		cell.Color = "error"
	case pct >= 75:
		cell.Color = "warn"
	}
	return cell
}
