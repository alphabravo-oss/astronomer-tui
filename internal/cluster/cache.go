//revive:disable:exported package-comments
package cluster

import (
	"context"
	"encoding/json"
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
}

type WatchPhase string

const (
	WatchSyncing WatchPhase = "syncing"
	WatchReady   WatchPhase = "ready"
	WatchStale   WatchPhase = "stale"
	WatchStopped WatchPhase = "stopped"
)

type WatchState struct {
	Phase     WatchPhase
	Err       error
	UpdatedAt time.Time
}

type CacheOptions struct {
	Namespace      string
	AllNamespaces  bool
	MaxObjects     int
	MaxObjectBytes int
}

type Subscription struct {
	Events <-chan Event
	Cancel func()
}

type informerKey struct {
	gvr        schema.GroupVersionResource
	namespaced bool
}

type ResourceCache struct {
	namespacedFactory dynamicinformer.DynamicSharedInformerFactory
	clusterFactory    dynamicinformer.DynamicSharedInformerFactory
	informers         map[informerKey]cache.SharedIndexInformer
	running           map[informerKey]struct{}
	subs              map[informerKey]map[uint64]chan Event
	states            map[informerKey]WatchState
	stop              <-chan struct{}
	options           CacheOptions
	nextSubID         uint64
	mu                sync.RWMutex
}

func NewResourceCache(client dynamic.Interface, resync time.Duration) *ResourceCache {
	return NewResourceCacheWithOptions(client, resync, CacheOptions{AllNamespaces: true})
}

func NewResourceCacheWithOptions(client dynamic.Interface, resync time.Duration, options CacheOptions) *ResourceCache {
	if options.MaxObjects <= 0 {
		options.MaxObjects = 10_000
	}
	if options.MaxObjectBytes <= 0 {
		options.MaxObjectBytes = 2 << 20
	}
	namespace := options.Namespace
	if options.AllNamespaces {
		namespace = ""
	}
	return &ResourceCache{
		namespacedFactory: dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, resync, namespace, nil),
		clusterFactory:    dynamicinformer.NewDynamicSharedInformerFactory(client, resync),
		informers:         make(map[informerKey]cache.SharedIndexInformer),
		running:           make(map[informerKey]struct{}),
		subs:              make(map[informerKey]map[uint64]chan Event),
		states:            make(map[informerKey]WatchState),
		options:           options,
	}
}

func (r *ResourceCache) Start(ctx context.Context) {
	r.mu.Lock()
	r.stop = ctx.Done()
	for key, informer := range r.informers {
		r.startLocked(key, informer)
	}
	r.mu.Unlock()
	go func() {
		<-ctx.Done()
		r.mu.Lock()
		for key := range r.running {
			r.states[key] = WatchState{Phase: WatchStopped, UpdatedAt: time.Now()}
		}
		r.mu.Unlock()
	}()
}

func (r *ResourceCache) Ensure(gvr schema.GroupVersionResource) cache.SharedIndexInformer {
	return r.EnsureFor(gvr, true)
}

func (r *ResourceCache) EnsureFor(gvr schema.GroupVersionResource, namespaced bool) cache.SharedIndexInformer {
	key := informerKey{gvr: gvr, namespaced: namespaced}
	r.mu.Lock()
	defer r.mu.Unlock()
	if informer, ok := r.informers[key]; ok {
		return informer
	}
	factory := r.clusterFactory
	if namespaced {
		factory = r.namespacedFactory
	}
	informer := factory.ForResource(gvr).Informer()
	_ = informer.SetWatchErrorHandlerWithContext(func(_ context.Context, _ *cache.Reflector, err error) {
		r.setState(key, WatchState{Phase: WatchStale, Err: err, UpdatedAt: time.Now()})
	})
	_, _ = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) { r.publish(key, Event{Type: EventAdded, Object: asUnstructured(obj)}) },
		UpdateFunc: func(_, newObj any) {
			r.publish(key, Event{Type: EventUpdated, Object: asUnstructured(newObj)})
		},
		DeleteFunc: func(obj any) { r.publish(key, Event{Type: EventDeleted, Object: asUnstructured(obj)}) },
	})
	r.informers[key] = informer
	r.states[key] = WatchState{Phase: WatchSyncing, UpdatedAt: time.Now()}
	if r.stop != nil {
		r.startLocked(key, informer)
	}
	return informer
}

func (r *ResourceCache) startLocked(key informerKey, informer cache.SharedIndexInformer) {
	if _, ok := r.running[key]; ok {
		return
	}
	r.running[key] = struct{}{}
	go informer.Run(r.stop)
}

func (r *ResourceCache) WaitForSync(ctx context.Context, gvr schema.GroupVersionResource) bool {
	return r.WaitForSyncFor(ctx, gvr, true)
}

func (r *ResourceCache) WaitForSyncFor(ctx context.Context, gvr schema.GroupVersionResource, namespaced bool) bool {
	key := informerKey{gvr: gvr, namespaced: namespaced}
	informer := r.EnsureFor(gvr, namespaced)
	ok := cache.WaitForCacheSync(ctx.Done(), informer.HasSynced)
	if ok {
		r.setState(key, WatchState{Phase: WatchReady, UpdatedAt: time.Now()})
	}
	return ok
}

func (r *ResourceCache) List(gvr schema.GroupVersionResource, namespace string, selector labels.Selector) ([]*unstructured.Unstructured, error) {
	return r.ListFor(gvr, true, namespace, selector)
}

func (r *ResourceCache) ListFor(gvr schema.GroupVersionResource, namespaced bool, namespace string, selector labels.Selector) ([]*unstructured.Unstructured, error) {
	informer := r.EnsureFor(gvr, namespaced)
	indexer := informer.GetIndexer()
	var raw []any
	var err error
	if namespaced && namespace != "" {
		raw, err = indexer.ByIndex(cache.NamespaceIndex, namespace)
	} else {
		raw = indexer.List()
	}
	if err != nil {
		return nil, err
	}
	out := make([]*unstructured.Unstructured, 0, min(len(raw), r.options.MaxObjects))
	for _, obj := range raw {
		u := asUnstructured(obj)
		if u == nil || !r.withinObjectBudget(u) {
			continue
		}
		if selector != nil && !selector.Matches(labels.Set(u.GetLabels())) {
			continue
		}
		out = append(out, u)
		if len(out) == r.options.MaxObjects {
			break
		}
	}
	return out, nil
}

func (r *ResourceCache) Subscribe(gvr schema.GroupVersionResource) <-chan Event {
	return r.SubscribeFor(gvr, true).Events
}

func (r *ResourceCache) SubscribeFor(gvr schema.GroupVersionResource, namespaced bool) Subscription {
	key := informerKey{gvr: gvr, namespaced: namespaced}
	ch := make(chan Event, 256)
	r.mu.Lock()
	r.nextSubID++
	id := r.nextSubID
	if r.subs[key] == nil {
		r.subs[key] = make(map[uint64]chan Event)
	}
	r.subs[key][id] = ch
	r.mu.Unlock()
	r.EnsureFor(gvr, namespaced)
	var once sync.Once
	return Subscription{Events: ch, Cancel: func() {
		once.Do(func() {
			r.mu.Lock()
			delete(r.subs[key], id)
			r.mu.Unlock()
		})
	}}
}

func (r *ResourceCache) State(gvr schema.GroupVersionResource, namespaced bool) WatchState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.states[informerKey{gvr: gvr, namespaced: namespaced}]
}

func (r *ResourceCache) setState(key informerKey, state WatchState) {
	r.mu.Lock()
	r.states[key] = state
	r.mu.Unlock()
}

func (r *ResourceCache) publish(key informerKey, event Event) {
	if event.Object == nil || !r.withinObjectBudget(event.Object) {
		return
	}
	r.mu.RLock()
	subs := make([]chan Event, 0, len(r.subs[key]))
	for _, ch := range r.subs[key] {
		subs = append(subs, ch)
	}
	r.mu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- event:
		default:
		}
	}
}

func (r *ResourceCache) withinObjectBudget(obj *unstructured.Unstructured) bool {
	data, err := json.Marshal(obj.Object)
	return err == nil && len(data) <= r.options.MaxObjectBytes
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
	resources, err := DiscoverResourceInfo(client)
	out := make([]schema.GroupVersionResource, 0, len(resources))
	for _, resource := range resources {
		out = append(out, resource.GVR)
	}
	return out, err
}

func containsSlash(s string) bool {
	for _, r := range s {
		if r == '/' {
			return true
		}
	}
	return false
}
