//revive:disable:exported package-comments
package cluster

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

type ResourceCache struct {
	factory   dynamicinformer.DynamicSharedInformerFactory
	informers map[schema.GroupVersionResource]cache.SharedIndexInformer
	running   map[schema.GroupVersionResource]struct{}
	subs      map[schema.GroupVersionResource][]chan Event
	stop      <-chan struct{}
	mu        sync.RWMutex
}

func NewResourceCache(client dynamic.Interface, resync time.Duration) *ResourceCache {
	return &ResourceCache{
		factory:   dynamicinformer.NewDynamicSharedInformerFactory(client, resync),
		informers: make(map[schema.GroupVersionResource]cache.SharedIndexInformer),
		running:   make(map[schema.GroupVersionResource]struct{}),
		subs:      make(map[schema.GroupVersionResource][]chan Event),
	}
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

// HasInformer reports whether an informer for the GVR already exists and has
// completed its initial sync. Used to avoid starting informers just to read a
// relationship column.
func (r *ResourceCache) HasInformer(gvr schema.GroupVersionResource) bool {
	r.mu.RLock()
	informer, ok := r.informers[gvr]
	r.mu.RUnlock()
	return ok && informer.HasSynced()
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
	ch := make(chan Event, 256)
	r.mu.Lock()
	r.subs[gvr] = append(r.subs[gvr], ch)
	r.mu.Unlock()
	r.Ensure(gvr)
	return ch
}

func (r *ResourceCache) publish(gvr schema.GroupVersionResource, event Event) {
	r.mu.RLock()
	subs := append([]chan Event(nil), r.subs[gvr]...)
	r.mu.RUnlock()
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

type ResolvedResource struct {
	GVR        schema.GroupVersionResource
	Namespaced bool
}

// ResolveResource maps a user-typed resource name (plural, singular, short
// name, kind, or plural.group) to a served GVR via API discovery. This is what
// lets `:mycrds` work for any installed CRD or aggregated API.
func ResolveResource(disco interface {
	ServerPreferredResources() ([]*metav1.APIResourceList, error)
}, name string) (ResolvedResource, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || disco == nil {
		return ResolvedResource{}, false
	}
	lists, err := disco.ServerPreferredResources()
	if err != nil && len(lists) == 0 {
		return ResolvedResource{}, false
	}
	for _, list := range lists {
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil {
			continue
		}
		for _, resource := range list.APIResources {
			if containsSlash(resource.Name) {
				continue
			}
			if resourceMatches(resource, gv.Group, name) {
				return ResolvedResource{GVR: gv.WithResource(resource.Name), Namespaced: resource.Namespaced}, true
			}
		}
	}
	return ResolvedResource{}, false
}

func resourceMatches(resource metav1.APIResource, group, name string) bool {
	if resource.Name == name || strings.ToLower(resource.SingularName) == name || strings.ToLower(resource.Kind) == name {
		return true
	}
	if group != "" && resource.Name+"."+group == name {
		return true
	}
	for _, short := range resource.ShortNames {
		if strings.ToLower(short) == name {
			return true
		}
	}
	return false
}

func containsSlash(s string) bool {
	for _, r := range s {
		if r == '/' {
			return true
		}
	}
	return false
}
