package cluster

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"time"
)

type Usage struct {
	CPUMilli, MemoryBytes int64
	Timestamp             time.Time
}

func FetchMetrics(ctx context.Context, client dynamic.Interface, kind, namespace string) (map[string]Usage, error) {
	g := schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: kind}
	var list *unstructured.UnstructuredList
	var err error
	if kind == "pods" && namespace != "" {
		list, err = client.Resource(g).Namespace(namespace).List(ctx, metav1.ListOptions{})
	} else {
		list, err = client.Resource(g).List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		return nil, fmt.Errorf("metrics unavailable: %w", err)
	}
	out := map[string]Usage{}
	for _, obj := range list.Items {
		stamp, _, _ := unstructured.NestedString(obj.Object, "timestamp")
		t, _ := time.Parse(time.RFC3339Nano, stamp)
		u := Usage{Timestamp: t}
		add := func(m map[string]any) {
			cpu, _ := m["cpu"].(string)
			mem, _ := m["memory"].(string)
			c, ce := resource.ParseQuantity(cpu)
			v, ve := resource.ParseQuantity(mem)
			if ce == nil {
				u.CPUMilli += c.MilliValue()
			}
			if ve == nil {
				u.MemoryBytes += v.Value()
			}
		}
		if kind == "pods" {
			cs, _, _ := unstructured.NestedSlice(obj.Object, "containers")
			for _, raw := range cs {
				m, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				usage, _, _ := unstructured.NestedMap(m, "usage")
				add(usage)
			}
		} else {
			usage, _, _ := unstructured.NestedMap(obj.Object, "usage")
			add(usage)
		}
		out[obj.GetNamespace()+"/"+obj.GetName()] = u
	}
	return out, nil
}
