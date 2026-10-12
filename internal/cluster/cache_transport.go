package cluster

import (
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/watchlist"
)

// A successful relist clears a stale reflector error even when the list is empty.
type reportingClient struct {
	dynamic.Interface
	success func(schema.GroupVersionResource)
}

func (c reportingClient) Resource(g schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return reportingNamespaced{NamespaceableResourceInterface: c.Interface.Resource(g), success: func() { c.success(g) }}
}

type reportingNamespaced struct {
	dynamic.NamespaceableResourceInterface
	success func()
}

func (r reportingNamespaced) Namespace(ns string) dynamic.ResourceInterface {
	return reportingResource{ResourceInterface: r.NamespaceableResourceInterface.Namespace(ns), success: r.success}
}
func (r reportingNamespaced) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	list, err := r.NamespaceableResourceInterface.List(ctx, opts)
	if err == nil {
		r.success()
	}
	return list, err
}

type reportingResource struct {
	dynamic.ResourceInterface
	success func()
}

func (r reportingResource) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	list, err := r.ResourceInterface.List(ctx, opts)
	if err == nil {
		r.success()
	}
	return list, err
}

func (c reportingClient) IsWatchListSemanticsUnSupported() bool {
	return watchlist.DoesClientNotSupportWatchListSemantics(c.Interface)
}
func (r reportingResource) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	w, err := r.ResourceInterface.Watch(ctx, opts)
	if err == nil {
		r.success()
	}
	return w, err
}
func (r reportingNamespaced) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	w, err := r.NamespaceableResourceInterface.Watch(ctx, opts)
	if err == nil {
		r.success()
	}
	return w, err
}
