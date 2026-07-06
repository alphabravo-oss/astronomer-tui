//revive:disable:exported package-comments
package cluster

import (
	"context"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/alphabravo-oss/astrotui/internal/ui/views"
)

var (
	podMetricsGVR  = schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "pods"}
	nodeMetricsGVR = schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "nodes"}
)

// MetricsPoller periodically reads metrics.k8s.io for pods and nodes via the
// dynamic client and caches the latest snapshot. It degrades silently when the
// metrics API is unavailable (no metrics-server), re-probing on a slow cadence.
type MetricsPoller struct {
	dyn      dynamic.Interface
	interval time.Duration

	mu        sync.RWMutex
	pods      map[string]views.PodMetrics
	nodes     map[string]views.NodeMetrics
	available bool

	subs []chan struct{}
}

const (
	defaultMetricsInterval = 15 * time.Second
	minMetricsInterval     = 5 * time.Second
	reprobeInterval        = 5 * time.Minute
)

// NewMetricsPoller builds a poller. An interval below the floor is clamped.
func NewMetricsPoller(dyn dynamic.Interface, interval time.Duration) *MetricsPoller {
	if interval <= 0 {
		interval = defaultMetricsInterval
	}
	if interval < minMetricsInterval {
		interval = minMetricsInterval
	}
	return &MetricsPoller{
		dyn:      dyn,
		interval: interval,
		pods:     map[string]views.PodMetrics{},
		nodes:    map[string]views.NodeMetrics{},
	}
}

// Start begins polling until ctx is cancelled. It polls immediately, then on
// the configured interval while metrics are available, falling back to a slow
// re-probe when they are not.
func (m *MetricsPoller) Start(ctx context.Context) {
	if m == nil || m.dyn == nil {
		return
	}
	go func() {
		for {
			ok := m.pollOnce(ctx)
			wait := m.interval
			if !ok {
				wait = reprobeInterval
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
	}()
}

// pollOnce fetches both metrics kinds and updates the snapshot. Returns whether
// the metrics API responded.
func (m *MetricsPoller) pollOnce(ctx context.Context) bool {
	reqCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	podList, podErr := m.dyn.Resource(podMetricsGVR).List(reqCtx, ListOptions())
	nodeList, nodeErr := m.dyn.Resource(nodeMetricsGVR).List(reqCtx, ListOptions())
	if podErr != nil && nodeErr != nil {
		m.mu.Lock()
		m.available = false
		m.mu.Unlock()
		return false
	}

	pods := map[string]views.PodMetrics{}
	if podErr == nil {
		for i := range podList.Items {
			parsePodMetrics(&podList.Items[i], pods)
		}
	}
	nodes := map[string]views.NodeMetrics{}
	if nodeErr == nil {
		for i := range nodeList.Items {
			parseNodeMetrics(&nodeList.Items[i], nodes)
		}
	}

	m.mu.Lock()
	m.pods = pods
	m.nodes = nodes
	m.available = true
	subs := append([]chan struct{}(nil), m.subs...)
	m.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	return true
}

// Snapshot returns the latest metrics maps, node allocatable (empty here; the
// app fills allocatable from the nodes informer), and availability.
func (m *MetricsPoller) Snapshot() (map[string]views.PodMetrics, map[string]views.NodeMetrics, views.NodeAllocatable, bool) {
	if m == nil {
		return nil, nil, views.NodeAllocatable{}, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pods, m.nodes, views.NodeAllocatable{}, m.available
}

// Subscribe returns a channel signalled after each successful poll.
func (m *MetricsPoller) Subscribe() <-chan struct{} {
	ch := make(chan struct{}, 1)
	m.mu.Lock()
	m.subs = append(m.subs, ch)
	m.mu.Unlock()
	return ch
}

func parsePodMetrics(item *unstructured.Unstructured, out map[string]views.PodMetrics) {
	name := item.GetName()
	ns := item.GetNamespace()
	containers, ok, _ := unstructured.NestedSlice(item.Object, "containers")
	if !ok {
		return
	}
	pm := views.PodMetrics{Containers: map[string]views.ContainerMetrics{}}
	for _, raw := range containers {
		c, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		cpu := quantityMilli(nestedStr(c, "usage", "cpu"))
		mem := quantityBytes(nestedStr(c, "usage", "memory"))
		pm.CPUMilli += cpu
		pm.MemBytes += mem
		if cname := stringField(c, "name"); cname != "" {
			pm.Containers[cname] = views.ContainerMetrics{CPUMilli: cpu, MemBytes: mem}
		}
	}
	out[ns+"/"+name] = pm
}

func parseNodeMetrics(item *unstructured.Unstructured, out map[string]views.NodeMetrics) {
	name := item.GetName()
	out[name] = views.NodeMetrics{
		CPUMilli: quantityMilli(nestedStr(item.Object, "usage", "cpu")),
		MemBytes: quantityBytes(nestedStr(item.Object, "usage", "memory")),
	}
}

func nestedStr(obj map[string]any, path ...string) string {
	v, ok, _ := unstructured.NestedString(obj, path...)
	if !ok {
		return ""
	}
	return v
}

func stringField(obj map[string]any, key string) string {
	if v, ok := obj[key].(string); ok {
		return v
	}
	return ""
}

// quantityMilli parses a Kubernetes CPU quantity into milli-cores.
func quantityMilli(s string) int64 {
	if s == "" {
		return 0
	}
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return 0
	}
	return q.MilliValue()
}

// quantityBytes parses a Kubernetes memory quantity into bytes.
func quantityBytes(s string) int64 {
	if s == "" {
		return 0
	}
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return 0
	}
	return q.Value()
}
