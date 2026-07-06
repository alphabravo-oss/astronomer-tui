package ui

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/palette"
)

func TestRecentCommandEntries(t *testing.T) {
	app := New(Options{})
	app.pushHistory(":pods")
	app.pushHistory(":svc")
	entries := app.recentCommandEntries()
	if len(entries) != 2 || entries[0].Title != ":svc" || entries[0].Category != palette.Recent {
		t.Fatalf("unexpected recent entries %#v", entries)
	}
}

func TestCrossInformerEntriesIndexesOtherResources(t *testing.T) {
	podGVR := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(),
		podObject("api", "default"), podObject("db", "default"))
	app := New(Options{Client: &cluster.Client{Namespace: "default", Dynamic: client}})
	app.cache = cluster.NewResourceCache(client, 0)
	ctx := t.Context()
	app.cache.Start(ctx)
	app.cache.Ensure(podGVR)
	app.cache.WaitForSync(ctx, podGVR)

	// current = something else so pods are indexed as cross-informer jumps.
	entries := app.crossInformerEntries(schema.GroupVersionResource{Version: "v1", Resource: "services"})
	found := 0
	for _, e := range entries {
		if _, ok := e.Payload.(jumpTarget); ok {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("expected 2 pod jump entries, got %d (%#v)", found, entries)
	}
	// When pods is the current view, they are not re-indexed here.
	if got := app.crossInformerEntries(podGVR); len(got) != 0 {
		t.Fatalf("expected no cross entries for current gvr, got %d", len(got))
	}
}
