package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/config"
	"github.com/gdamore/tcell/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

func TestGraphUsesOwnerUIDAndNamespace(t *testing.T) {
	g := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	owner := podObject("owner", "one")
	owner.SetUID("current")
	child := podObject("child", "one")
	child.SetUID("child")
	child.SetOwnerReferences([]metav1.OwnerReference{{Name: "owner", UID: "old"}})
	if len(graphChildren(cluster.GraphObject{GVR: g, Object: owner}, []cluster.GraphObject{{GVR: g, Object: child}})) != 0 {
		t.Fatal("same-name replacement linked")
	}
	child.SetOwnerReferences([]metav1.OwnerReference{{Name: "owner", UID: "current"}})
	if len(graphChildren(cluster.GraphObject{GVR: g, Object: owner}, []cluster.GraphObject{{GVR: g, Object: child}})) != 1 {
		t.Fatal("owner UID not linked")
	}
	child.SetNamespace("two")
	if len(graphChildren(cluster.GraphObject{GVR: g, Object: owner}, []cluster.GraphObject{{GVR: g, Object: child}})) != 0 {
		t.Fatal("cross-namespace ownership")
	}
}
func TestPodReferenceSources(t *testing.T) {
	p := podObject("api", "one")
	p.Object["spec"] = map[string]any{"serviceAccountName": "runner", "containers": []any{map[string]any{"name": "api", "envFrom": []any{map[string]any{"secretRef": map[string]any{"name": "credentials"}}}}}, "volumes": []any{map[string]any{"name": "config", "configMap": map[string]any{"name": "settings"}}}}
	refs := podReferences(p)
	if len(refs) != 3 {
		t.Fatalf("%+v", refs)
	}
}
func TestReadOnlyBlocksNewMutationEntrypoints(t *testing.T) {
	a := qualityApp(t)
	a.options.Config = config.Default()
	a.options.Config.ReadOnly = true
	screen := startQualityScreen(t, a, 100, 30)
	called := false
	a.app.QueueUpdateDraw(func() { a.confirmExternalMutation("New action", "Confirm", func() { called = true }) })
	if !waitForScreenText(t, a, screen, "read-only") {
		t.Fatal("missing denial")
	}
	if called {
		t.Fatal("readonly mutation executed")
	}
}
func TestMarkedSelectionPersistsSortByUID(t *testing.T) {
	a := qualityApp(t)
	one, two := podObject("one", "default"), podObject("two", "default")
	one.SetUID(types.UID("one"))
	two.SetUID(types.UID("two"))
	a.objects = []*unstructured.Unstructured{one, two}
	a.renderCurrent()
	a.table.Select(2, 0)
	a.toggleMarked()
	a.setSort(0)
	if len(a.marked) != 1 || a.marked["two"] == nil {
		t.Fatal("mark moved with sort")
	}
	if !strings.Contains(a.status.GetText(false), "1 marked") {
		t.Fatal("marked targets are invisible")
	}
}
func TestArchiveCommandAndExplorerPalette(t *testing.T) {
	a := qualityApp(t)
	a.options.ConfigDir = t.TempDir()
	screen := startQualityScreen(t, a, 100, 30)
	a.app.QueueUpdateDraw(func() { a.specialCommand("sd") })
	if !waitForScreenText(t, a, screen, "No saved files") {
		t.Fatal("archive unreachable")
	}
	found := map[string]bool{}
	a.app.QueueUpdateDraw(func() {
		for _, e := range a.paletteEntries() {
			found[e.ID] = true
		}
	})
	for _, cmd := range []string{"pulses", "xray", "rbac", "bench", "helm"} {
		if !found["view:"+cmd] {
			t.Fatal("missing palette command", cmd)
		}
	}
	a.app.QueueUpdateDraw(a.showMain)
	a.app.QueueEvent(tcell.NewEventKey(tcell.KeyCtrlA, 0, 0))
	if !waitForScreenText(t, a, screen, "Resource aliases") {
		t.Fatal("alias list unreachable")
	}
}
func TestAuxiliaryContextIsCancelledOnReturn(t *testing.T) {
	a := qualityApp(t)
	ctx := a.auxiliaryContext()
	a.showMain()
	if ctx.Err() != context.Canceled {
		t.Fatal("auxiliary view polling leaked")
	}
}
