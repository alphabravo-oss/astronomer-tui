package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type catalogDiscovery struct {
	fake.FakeDiscovery
	lists []*metav1.APIResourceList
	err   error
}

func (d catalogDiscovery) ServerPreferredResources() ([]*metav1.APIResourceList, error) {
	return d.lists, d.err
}
func TestCatalogPreferredAliasesScopeAndAmbiguity(t *testing.T) {
	lists := []*metav1.APIResourceList{{GroupVersion: "example.io/v1", APIResources: []metav1.APIResource{{Name: "widgets", SingularName: "widget", Kind: "Widget", ShortNames: []string{"wi"}, Namespaced: false, Verbs: metav1.Verbs{"list", "watch"}}, {Name: "widgets/status", Verbs: metav1.Verbs{"list", "watch"}}, {Name: "readonly", Verbs: metav1.Verbs{"list"}}}}}
	c, err := LoadCatalog(&catalogDiscovery{lists: lists})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"wi", "Widget", "widget", "widgets.example.io", "example.io/v1/widgets"} {
		g, err := c.Resolve(name)
		if err != nil || g.Resource != "widgets" {
			t.Fatalf("%s: %v", name, err)
		}
		r, _ := c.Lookup(g)
		if r.Namespaced {
			t.Fatal("scope lost")
		}
	}
	if len(c.Resources) != 1 {
		t.Fatal(c.Resources)
	}
	lists = append(lists, &metav1.APIResourceList{GroupVersion: "other.io/v2", APIResources: []metav1.APIResource{{Name: "widgets", Kind: "Widget", Verbs: metav1.Verbs{"list", "watch"}}}})
	c = CatalogFromLists(lists)
	if _, err = c.Resolve("widget"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatal(err)
	}
	if _, err = c.Resolve("missing"); err == nil {
		t.Fatal("missing resolved")
	}
	if c, e := LoadCatalog(&catalogDiscovery{err: fmt.Errorf("offline")}); e == nil || c != nil {
		t.Fatal("offline discovery accepted")
	}
	c, err = LoadCatalog(&catalogDiscovery{lists: lists, err: fmt.Errorf("partial")})
	if c == nil || err == nil {
		t.Fatal("partial results lost")
	}
}
func TestScopedCacheNeverListsAllNamespaces(t *testing.T) {
	g := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "one", "namespace": "allowed"}}}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj)
	client.PrependReactor("*", "pods", func(a ktesting.Action) (bool, runtime.Object, error) {
		if a.GetNamespace() != "allowed" {
			return true, nil, fmt.Errorf("cluster-wide access forbidden")
		}
		return false, nil, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cache := NewScopedResourceCache(client, 0, "allowed")
	cache.Start(ctx)
	if !cache.WaitForSync(ctx, g) {
		t.Fatal("scoped sync failed")
	}
	items, err := cache.List(g, "allowed", labels.Everything())
	if err != nil || len(items) != 1 {
		t.Fatalf("%v %d", err, len(items))
	}
	for _, a := range client.Actions() {
		if a.GetNamespace() != "allowed" {
			t.Fatalf("escaped scope: %v", a)
		}
	}
}
func TestMetricsQuantitiesAndUnavailable(t *testing.T) {
	for _, kind := range []string{"pods", "nodes"} {
		t.Run(kind, func(t *testing.T) {
			g := schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: kind}
			c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{g: "MetricsList"})
			c.PrependReactor("list", kind, func(a ktesting.Action) (bool, runtime.Object, error) {
				obj := map[string]any{"apiVersion": "metrics.k8s.io/v1beta1", "kind": "Metrics", "metadata": map[string]any{"name": "api", "namespace": "allowed"}, "timestamp": "2026-10-12T10:00:00Z"}
				usage := map[string]any{"cpu": "125000000n", "memory": "16Mi"}
				if kind == "pods" {
					obj["containers"] = []any{map[string]any{"usage": usage}, map[string]any{"usage": usage}}
				} else {
					obj["usage"] = usage
				}
				return true, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{{Object: obj}}}, nil
			})
			usage, err := FetchMetrics(context.Background(), c, kind, "allowed")
			if err != nil {
				t.Fatal(err)
			}
			u := usage["allowed/api"]
			mult := int64(1)
			if kind == "pods" {
				mult = 2
			}
			if u.CPUMilli != 125*mult || u.MemoryBytes != 16*1024*1024*mult || u.Timestamp.IsZero() {
				t.Fatal(u)
			}
			c.PrependReactor("list", kind, func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, fmt.Errorf("forbidden") })
			if _, err = FetchMetrics(context.Background(), c, kind, ""); err == nil {
				t.Fatal("missing unavailable error")
			}
		})
	}
}
func TestServerTableNegotiationAndFallback(t *testing.T) {
	status := 200
	kind := "Table"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept"), "as=Table") {
			t.Error("no negotiation")
		}
		if r.URL.Query().Get("includeObject") != "Metadata" {
			t.Error("missing identity")
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"kind": kind, "apiVersion": "meta.k8s.io/v1", "columnDefinitions": []any{map[string]any{"name": "Name", "type": "string"}}})
	}))
	defer server.Close()
	cfg := &rest.Config{Host: server.URL}
	g := schema.GroupVersionResource{Group: "example.io", Version: "v1", Resource: "widgets"}
	table, err := FetchTable(context.Background(), cfg, g, "allowed")
	if err != nil || len(table.ColumnDefinitions) != 1 {
		t.Fatal(err)
	}
	kind = "List"
	if _, err = FetchTable(context.Background(), cfg, g, ""); err == nil {
		t.Fatal("unsupported table accepted")
	}
	status = 403
	if _, err = FetchTable(context.Background(), cfg, g, ""); err == nil {
		t.Fatal("forbidden accepted")
	}
}

func TestEmptyRelistClearsWatchError(t *testing.T) {
	g := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{g: "PodList"})
	cache := NewResourceCache(client, 0)
	cache.publish(g, Event{Err: fmt.Errorf("temporary failure")})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cache.Start(ctx)
	if !cache.WaitForSync(ctx, g) {
		t.Fatal("empty list did not sync")
	}
	if cache.LastError(g) != nil {
		t.Fatal("recovered empty cache retained failure")
	}
}
