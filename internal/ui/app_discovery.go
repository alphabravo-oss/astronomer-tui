package ui

import (
	"context"
	"errors"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/ui/views"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func (a *App) resolveResource(name string) (schema.GroupVersionResource, error) {
	if g, ok := explicitGVRCommand(name); ok {
		return g, nil
	}
	if a.catalog != nil {
		g, err := a.catalog.Resolve(name)
		if err == nil {
			return g, nil
		}
		if errors.Is(err, cluster.ErrAmbiguous) {
			return g, err
		}
		if builtin, ok := resourceCommand(name); ok {
			if _, found := a.catalog.Lookup(builtin); found {
				return builtin, nil
			}
		}
		return g, err
	}
	if g, ok := resourceCommand(name); ok {
		return g, nil
	}
	return (*cluster.Catalog)(nil).Resolve(name)
}
func (a *App) resourceNamespace(g schema.GroupVersionResource) string {
	if r, ok := a.catalog.Lookup(g); ok {
		if !r.Namespaced {
			return ""
		}
		return a.options.Client.Namespace
	}
	return namespaceFor(a.options.Client, g)
}
func (a *App) ensureScope(g schema.GroupVersionResource) {
	if a.cacheCancel == nil || a.options.Client == nil {
		return
	}
	ns := a.resourceNamespace(g)
	// Only the active view owns a cache: resource and namespace changes release
	// the previous reflector/store rather than accumulating idle watches.
	if ns == a.cacheScope && g == a.currentGVR {
		return
	}
	a.cacheCancel()
	a.cacheScope = ns
	a.cache = cluster.NewScopedResourceCache(a.options.Client.Dynamic, 30*time.Minute, ns)
	ctx, cancel := context.WithCancel(a.backgroundContext())
	a.cacheCancel = cancel
	a.cache.Start(ctx)
}
func (a *App) refreshDiscovery() {
	client := a.options.Client
	if client == nil || client.Discovery == nil {
		return
	}
	a.discoveryGeneration++
	revision := a.discoveryGeneration
	go func() {
		client.Discovery.Invalidate()
		catalog, err := cluster.LoadCatalog(client.Discovery)
		ctx, cancel := context.WithTimeout(a.backgroundContext(), 10*time.Second)
		defer cancel()
		namespaces, _ := cluster.ListNamespaces(ctx, client)
		renderers := map[schema.GroupVersionResource]views.Renderer{}
		if client.Dynamic != nil {
			crds, listErr := client.Dynamic.Resource(schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}).List(ctx, metav1.ListOptions{})
			if listErr == nil {
				for _, obj := range crds.Items {
					var crd apiextensionsv1.CustomResourceDefinition
					if runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &crd) != nil {
						continue
					}
					for _, v := range crd.Spec.Versions {
						if v.Served {
							r := views.RendererForCRD(&crd, v.Name)
							renderers[r.GVR()] = r
						}
					}
				}
			}
		}
		a.queueUpdate(a.backgroundContext(), func() {
			if a.options.Client != client || a.discoveryGeneration != revision {
				return
			}
			a.discoveryError = err
			a.namespaceCatalog = namespaces
			if catalog == nil {
				return
			}
			a.catalog = catalog
			a.renderers = renderers
			filter := a.filter
			a.switchResource(a.backgroundContext(), a.currentGVR)
			a.filter = filter
			a.reloadCurrent()
		})
	}()
}

func (a *App) watchServerTable(ctx context.Context) {
	client, g, generation := a.options.Client, a.currentGVR, a.generation
	if client == nil || client.Config == nil {
		return
	}
	if _, builtin := views.Builtins()[g]; builtin {
		return
	}
	if a.renderers[g] != nil {
		return
	}
	base, ns := a.renderer, a.resourceNamespace(g)
	go func() {
		for {
			work, cancel := context.WithTimeout(ctx, 5*time.Second)
			table, err := cluster.FetchTable(work, client.Config, g, ns)
			cancel()
			if err == nil {
				a.queueUpdate(ctx, func() {
					if a.generation == generation {
						a.renderer = views.NewTableRenderer(base, table)
						a.renderCache = nil
						a.renderCurrent()
					}
				})
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
}
