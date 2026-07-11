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

func TestResourceCacheNamespaceScopeAndObjectBudget(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(),
		podForCache("api", "default", "small"),
		podForCache("other", "kube-system", "small"),
		podForCache("huge", "default", string(make([]byte, 2048))),
	)
	resourceCache := NewResourceCacheWithOptions(client, time.Hour, CacheOptions{
		Namespace: "default", MaxObjects: 10, MaxObjectBytes: 1024,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resourceCache.Start(ctx)
	if !resourceCache.WaitForSyncFor(ctx, gvr, true) {
		t.Fatal("cache did not sync")
	}
	items, err := resourceCache.ListFor(gvr, true, "default", labels.Everything())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].GetName() != "api" {
		t.Fatalf("expected only bounded object in selected namespace, got %#v", items)
	}
	if state := resourceCache.State(gvr, true); state.Phase != WatchReady {
		t.Fatalf("expected ready watch, got %#v", state)
	}
}

func TestResourceCacheSubscriptionCanBeCancelled(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	resourceCache := NewResourceCache(dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), time.Hour)
	subscription := resourceCache.SubscribeFor(gvr, true)
	subscription.Cancel()
	subscription.Cancel()
	resourceCache.publish(informerKey{gvr: gvr, namespaced: true}, Event{Type: EventAdded, Object: podForCache("api", "default", "small")})
	select {
	case event := <-subscription.Events:
		t.Fatalf("cancelled subscription received event %#v", event)
	default:
	}
}

func podForCache(name, namespace, value string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": name, "namespace": namespace},
		"spec":     map[string]any{"value": value},
	}}
}
