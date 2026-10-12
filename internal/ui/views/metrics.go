package views

import (
	"fmt"
	"github.com/alphabravo-oss/astrotui/internal/cluster"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	resourcehelper "k8s.io/component-helpers/resource"
	"time"
)

type MetricsRenderer struct {
	Renderer
	Usage map[string]cluster.Usage
	Error error
	Now   time.Time
}

func (m MetricsRenderer) Row(obj *unstructured.Unstructured) []Cell {
	row := m.Renderer.Row(obj)
	u, ok := m.Usage[obj.GetNamespace()+"/"+obj.GetName()]
	state := ""
	if !ok || m.Error != nil {
		state = "n/a"
	} else if u.Timestamp.IsZero() || m.Now.Sub(u.Timestamp) > 2*time.Minute {
		state = "stale"
	}
	cpuReq, memReq, cpuLim, memLim := int64(0), int64(0), int64(0), int64(0)
	quantity := func(o map[string]any, path ...string) int64 {
		s, _, _ := unstructured.NestedString(o, path...)
		q, err := resource.ParseQuantity(s)
		if err != nil {
			return 0
		}
		if path[len(path)-1] == "cpu" {
			return q.MilliValue()
		}
		return q.Value()
	}
	var pod corev1.Pod
	if obj.GetKind() == "Pod" && runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &pod) == nil {
		requests := resourcehelper.PodRequests(&pod, resourcehelper.PodResourcesOptions{})
		limits := resourcehelper.PodLimits(&pod, resourcehelper.PodResourcesOptions{})
		cpuReq = requests.Cpu().MilliValue()
		memReq = requests.Memory().Value()
		cpuLim = limits.Cpu().MilliValue()
		memLim = limits.Memory().Value()
	}
	cpuA := quantity(obj.Object, "status", "allocatable", "cpu")
	memA := quantity(obj.Object, "status", "allocatable", "memory")
	percent := func(used, total int64) string {
		if total <= 0 {
			return "—"
		}
		return fmt.Sprintf("%.0f%%", float64(used)*100/float64(total))
	}
	values := map[string]string{"CPU": fmt.Sprintf("%dm", u.CPUMilli), "MEM": fmt.Sprintf("%dMi", u.MemoryBytes/(1024*1024)), "CPU/RL": fmt.Sprintf("%dm/%dm", cpuReq, cpuLim), "MEM/RL": fmt.Sprintf("%dMi/%dMi", memReq/(1024*1024), memLim/(1024*1024)), "%CPU/R": percent(u.CPUMilli, cpuReq), "%CPU/L": percent(u.CPUMilli, cpuLim), "%MEM/R": percent(u.MemoryBytes, memReq), "%MEM/L": percent(u.MemoryBytes, memLim), "%CPU": percent(u.CPUMilli, cpuA), "%MEM": percent(u.MemoryBytes, memA), "CPU/A": fmt.Sprintf("%dm", cpuA), "MEM/A": fmt.Sprintf("%dMi", memA/(1024*1024))}
	for i, c := range m.Header() {
		if value, found := values[c.Name]; found {
			if state != "" && (c.Name == "CPU" || c.Name == "MEM" || c.Name[0] == '%') {
				value = state
			}
			row[i] = Cell{Text: value}
		}
	}
	return row
}
