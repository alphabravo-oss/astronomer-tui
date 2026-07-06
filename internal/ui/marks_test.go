package ui

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/ui/views"
)

func TestMarkedObjectsTracksVisible(t *testing.T) {
	app := New(Options{})
	app.visibleObjects = []*unstructured.Unstructured{
		namedObject("api", "default"),
		namedObject("db", "default"),
		namedObject("worker", "default"),
	}
	app.marks = map[string]struct{}{"default/api": {}, "default/worker": {}}
	got := app.markedObjects()
	if len(got) != 2 || got[0].GetName() != "api" || got[1].GetName() != "worker" {
		t.Fatalf("unexpected marked objects %#v", got)
	}
	if app.markCount() != 2 {
		t.Fatalf("expected mark count 2, got %d", app.markCount())
	}
}

func TestRunBulkDeleteDeletesAllAndClearsMarks(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(),
		podObject("api", "default"), podObject("db", "default"))
	app := New(Options{Client: &cluster.Client{Context: "kind-kind", Namespace: "default", Dynamic: client}})
	app.renderer = views.GenericRenderer{Resource: gvr}
	app.marks = map[string]struct{}{"default/api": {}, "default/db": {}}

	objs := []*unstructured.Unstructured{podObject("api", "default"), podObject("db", "default")}
	app.runBulkDelete(gvr, objs, "")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	list, err := client.Resource(gvr).Namespace("default").List(ctx, cluster.ListOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("expected all pods deleted, %d remain", len(list.Items))
	}
	if app.markCount() != 0 {
		t.Fatalf("expected marks cleared, got %d", app.markCount())
	}
}

func TestRunBulkDeleteBlockedByReadOnly(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), podObject("api", "prod"))
	app := New(Options{Client: &cluster.Client{Context: "prod-cluster", Namespace: "prod", Dynamic: client}})
	app.renderer = views.GenericRenderer{Resource: gvr}
	// prod-cluster matches the default prod regex -> confirm-destructive; wrong
	// confirmation text must block the delete.
	app.runBulkDelete(gvr, []*unstructured.Unstructured{podObject("api", "prod")}, "wrong")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	list, _ := client.Resource(gvr).Namespace("prod").List(ctx, cluster.ListOptions())
	if len(list.Items) != 1 {
		t.Fatalf("expected pod to survive failed confirmation, %d remain", len(list.Items))
	}
}
