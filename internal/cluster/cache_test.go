package cluster

import (
	"context"
	"fmt"
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

func TestSubscriptionCancellationAndWatchError(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	cache := NewResourceCache(dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), 0)
	ch, cancel := cache.SubscribeWithCancel(gvr)
	failure := fmt.Errorf("forbidden: pods")
	cache.publish(gvr, Event{Err: failure})
	if event := <-ch; event.Err != failure || cache.LastError(gvr) != failure {
		t.Fatal("watch failure not exposed")
	}
	cancel()
	cancel()
	cache.mu.RLock()
	count := len(cache.subs[gvr])
	cache.mu.RUnlock()
	if count != 0 {
		t.Fatal("abandoned subscription retained")
	}
	cache.publish(gvr, Event{Type: EventAdded})
	select {
	case <-ch:
		t.Fatal("cancelled subscriber still receives events")
	default:
	}
	if cache.LastError(gvr) != nil {
		t.Fatal("successful event did not clear error")
	}
}
