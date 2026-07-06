package cluster

import (
	"context"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

func metricsScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	s.AddKnownTypeWithName(schema.GroupVersionKind{Group: "metrics.k8s.io", Version: "v1beta1", Kind: "PodMetricsList"}, &unstructured.UnstructuredList{})
	s.AddKnownTypeWithName(schema.GroupVersionKind{Group: "metrics.k8s.io", Version: "v1beta1", Kind: "NodeMetricsList"}, &unstructured.UnstructuredList{})
	return s
}

func podMetric(ns, name string, containers ...map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "metrics.k8s.io/v1beta1",
		"kind":       "PodMetrics",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"containers": toAnySlice(containers),
	}}
}

func nodeMetric(name, cpu, mem string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "metrics.k8s.io/v1beta1",
		"kind":       "NodeMetrics",
		"metadata":   map[string]any{"name": name},
		"usage":      map[string]any{"cpu": cpu, "memory": mem},
	}}
}

func toAnySlice(in []map[string]any) []any {
	out := make([]any, len(in))
	for i := range in {
		out[i] = in[i]
	}
	return out
}

func container(name, cpu, mem string) map[string]any {
	return map[string]any{"name": name, "usage": map[string]any{"cpu": cpu, "memory": mem}}
}

func TestMetricsPollerParsesPodsAndNodes(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(metricsScheme(),
		map[schema.GroupVersionResource]string{
			podMetricsGVR:  "PodMetricsList",
			nodeMetricsGVR: "NodeMetricsList",
		})
	if err := client.Tracker().Create(podMetricsGVR, podMetric("default", "api", container("app", "150m", "64Mi"), container("side", "50m", "16Mi")), "default"); err != nil {
		t.Fatal(err)
	}
	if err := client.Tracker().Create(nodeMetricsGVR, nodeMetric("node-a", "1500m", "2Gi"), ""); err != nil {
		t.Fatal(err)
	}
	poller := NewMetricsPoller(client, time.Second)
	if ok := poller.pollOnce(context.Background()); !ok {
		t.Fatal("expected metrics poll to succeed")
	}
	pods, nodes, _, available := poller.Snapshot()
	if !available {
		t.Fatal("expected metrics available")
	}
	pm, ok := pods["default/api"]
	if !ok {
		t.Fatalf("missing pod metrics, have %#v", pods)
	}
	if pm.CPUMilli != 200 {
		t.Fatalf("expected 200m summed cpu, got %d", pm.CPUMilli)
	}
	if pm.MemBytes != (64+16)*1024*1024 {
		t.Fatalf("unexpected mem bytes %d", pm.MemBytes)
	}
	if c := pm.Containers["app"]; c.CPUMilli != 150 {
		t.Fatalf("unexpected container cpu %d", c.CPUMilli)
	}
	nm, ok := nodes["node-a"]
	if !ok || nm.CPUMilli != 1500 || nm.MemBytes != 2*1024*1024*1024 {
		t.Fatalf("unexpected node metrics %#v", nm)
	}
}

func TestMetricsPollerUnavailableWhenAPIMissing(t *testing.T) {
	// Metrics API present in the scheme but returning errors (no metrics-server).
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(metricsScheme(),
		map[schema.GroupVersionResource]string{
			podMetricsGVR:  "PodMetricsList",
			nodeMetricsGVR: "NodeMetricsList",
		})
	client.PrependReactor("list", "*", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Group: "metrics.k8s.io", Resource: "pods"}, "")
	})
	poller := NewMetricsPoller(client, time.Second)
	if ok := poller.pollOnce(context.Background()); ok {
		t.Fatal("expected poll to report unavailable")
	}
	_, _, _, available := poller.Snapshot()
	if available {
		t.Fatal("expected metrics unavailable")
	}
}

func TestNewMetricsPollerClampsInterval(t *testing.T) {
	p := NewMetricsPoller(dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), time.Millisecond)
	if p.interval < minMetricsInterval {
		t.Fatalf("expected interval clamped to %s, got %s", minMetricsInterval, p.interval)
	}
}
