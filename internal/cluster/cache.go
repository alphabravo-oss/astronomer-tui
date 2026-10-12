//revive:disable:exported package-comments
package cluster

import (
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicinformer "k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

type EventType string

const (
	EventAdded   EventType = "added"
	EventUpdated EventType = "updated"
	EventDeleted EventType = "deleted"
)

type Event struct {
	Type   EventType
	Object *unstructured.Unstructured
	Err    error
}

type ResourceCache struct {
	factory    dynamicinformer.DynamicSharedInformerFactory
	informers  map[schema.GroupVersionResource]cache.SharedIndexInformer
	running    map[schema.GroupVersionResource]struct{}
	subs       map[schema.GroupVersionResource][]chan Event
	lastErrors map[schema.GroupVersionResource]error
	stop       <-chan struct{}
	mu         sync.RWMutex
}

func NewResourceCache(client dynamic.Interface, resync time.Duration) *ResourceCache {
	return NewScopedResourceCache(client, resync, "")
}

// NewScopedResourceCache restricts LIST and WATCH at the API, not just the UI.
func NewScopedResourceCache(client dynamic.Interface, resync time.Duration, namespace string) *ResourceCache {
	r := &ResourceCache{

		informers:  make(map[schema.GroupVersionResource]cache.SharedIndexInformer),
		running:    make(map[schema.GroupVersionResource]struct{}),
		subs:       make(map[schema.GroupVersionResource][]chan Event),
		lastErrors: make(map[schema.GroupVersionResource]error),
	}
	r.factory = dynamicinformer.NewFilteredDynamicSharedInformerFactory(reportingClient{Interface: client, success: func(g schema.GroupVersionResource) {
		if r.LastError(g) != nil {
			r.publish(g, Event{})
		}
	}}, resync, namespace, nil)
	return r
}

func (r *ResourceCache) Start(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stop = ctx.Done()
	for gvr, informer := range r.informers {
		r.startLocked(gvr, informer)
	}
}

func (r *ResourceCache) Ensure(gvr schema.GroupVersionResource) cache.SharedIndexInformer {
	r.mu.Lock()
	defer r.mu.Unlock()
	if informer, ok := r.informers[gvr]; ok {
		return informer
	}
	informer := r.factory.ForResource(gvr).Informer()
	_ = informer.SetWatchErrorHandlerWithContext(func(_ context.Context, _ *cache.Reflector, err error) {
		r.publish(gvr, Event{Err: err})
	})
	_, _ = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			r.publish(gvr, Event{Type: EventAdded, Object: asUnstructured(obj)})
		},
		UpdateFunc: func(_, newObj any) {
			r.publish(gvr, Event{Type: EventUpdated, Object: asUnstructured(newObj)})
		},
		DeleteFunc: func(obj any) {
			r.publish(gvr, Event{Type: EventDeleted, Object: asUnstructured(obj)})
		},
	})
	r.informers[gvr] = informer
	if r.stop != nil {
		r.startLocked(gvr, informer)
	}
	return informer
}

func (r *ResourceCache) startLocked(gvr schema.GroupVersionResource, informer cache.SharedIndexInformer) {
	if _, ok := r.running[gvr]; ok {
		return
	}
	r.running[gvr] = struct{}{}
	go informer.Run(r.stop)
}

func (r *ResourceCache) WaitForSync(ctx context.Context, gvr schema.GroupVersionResource) bool {
	informer := r.Ensure(gvr)
	return cache.WaitForCacheSync(ctx.Done(), informer.HasSynced)
}

func (r *ResourceCache) List(gvr schema.GroupVersionResource, namespace string, selector labels.Selector) ([]*unstructured.Unstructured, error) {
	informer := r.Ensure(gvr)
	indexer := informer.GetIndexer()
	var raw []any
	var err error
	if namespace != "" {
		raw, err = indexer.ByIndex(cache.NamespaceIndex, namespace)
	} else {
		raw = indexer.List()
	}
	if err != nil {
		return nil, err
	}
	out := make([]*unstructured.Unstructured, 0, len(raw))
	for _, obj := range raw {
		u := asUnstructured(obj)
		if u == nil {
			continue
		}
		if selector != nil && !selector.Matches(labels.Set(u.GetLabels())) {
			continue
		}
		out = append(out, u)
	}
	return out, nil
}

func (r *ResourceCache) Subscribe(gvr schema.GroupVersionResource) <-chan Event {
	ch, _ := r.SubscribeWithCancel(gvr)
	return ch
}

// SubscribeWithCancel releases a subscription without closing a channel that
// an in-flight publisher might still be using. Cancellation is idempotent.
func (r *ResourceCache) SubscribeWithCancel(gvr schema.GroupVersionResource) (<-chan Event, func()) {
	ch := make(chan Event, 256)
	r.mu.Lock()
	r.subs[gvr] = append(r.subs[gvr], ch)
	r.mu.Unlock()
	r.Ensure(gvr)
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			for i, sub := range r.subs[gvr] {
				if sub == ch {
					r.subs[gvr] = append(r.subs[gvr][:i], r.subs[gvr][i+1:]...)
					break
				}
			}
		})
	}
}

func (r *ResourceCache) LastError(gvr schema.GroupVersionResource) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastErrors[gvr]
}

func (r *ResourceCache) publish(gvr schema.GroupVersionResource, event Event) {
	r.mu.Lock()
	r.lastErrors[gvr] = event.Err
	subs := append([]chan Event(nil), r.subs[gvr]...)
	r.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- event:
		default:
		}
	}
}

func asUnstructured(obj any) *unstructured.Unstructured {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil
	}
	return u
}

func DiscoverResources(client *Client) ([]schema.GroupVersionResource, error) {
	lists, err := client.Discovery.ServerPreferredResources()
	if err != nil && len(lists) == 0 {
		return nil, fmt.Errorf("discover resources: %w", err)
	}
	var out []schema.GroupVersionResource
	for _, list := range lists {
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil {
			continue
		}
		for _, resource := range list.APIResources {
			if len(resource.Verbs) == 0 || containsSlash(resource.Name) {
				continue
			}
			out = append(out, gv.WithResource(resource.Name))
		}
	}
	return out, nil
}

func containsSlash(s string) bool {
	for _, r := range s {
		if r == '/' {
			return true
		}
	}
	return false
}
