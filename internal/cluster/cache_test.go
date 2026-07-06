package cluster

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestResourceCacheListAndSubscribe(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":      "api",
			"namespace": "default",
			"labels": map[string]any{
				"app": "api",
			},
		},
	}}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj)
	cache := NewResourceCache(client, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events := cache.Subscribe(gvr)
	cache.Start(ctx)
	if !cache.WaitForSync(ctx, gvr) {
		t.Fatal("cache did not sync")
	}
	items, err := cache.List(gvr, "default", labels.SelectorFromSet(labels.Set{"app": "api"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].GetName() != "api" {
		t.Fatalf("unexpected list %#v", items)
	}
	select {
	case event := <-events:
		if event.Type != EventAdded || event.Object.GetName() != "api" {
			t.Fatalf("unexpected event %#v", event)
		}
	default:
		t.Fatal("expected add event")
	}
}

func TestResourceCacheStartsInformerCreatedAfterStart(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":      "api",
			"namespace": "default",
		},
	}}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj)
	cache := NewResourceCache(client, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cache.Start(ctx)
	if !cache.WaitForSync(ctx, gvr) {
		t.Fatal("cache did not sync for informer created after Start")
	}
	items, err := cache.List(gvr, "default", labels.Everything())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].GetName() != "api" {
		t.Fatalf("unexpected list %#v", items)
	}
}

func TestSubscribeErrorsReceivesPublishedErrors(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	cache := NewResourceCache(client, time.Hour)
	ch := cache.SubscribeErrors(gvr)
	cache.publishErr(gvr, context.DeadlineExceeded)
	select {
	case err := <-ch:
		if err == nil {
			t.Fatal("expected non-nil error")
		}
	case <-time.After(time.Second):
		t.Fatal("expected error to be delivered to subscriber")
	}
	// nil errors are dropped.
	cache.publishErr(gvr, nil)
	select {
	case <-ch:
		t.Fatal("nil error should not be published")
	case <-time.After(50 * time.Millisecond):
	}
}
