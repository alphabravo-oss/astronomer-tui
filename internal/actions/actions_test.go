package actions

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

var errTestConflict = errors.New("resource version changed")

func TestCatalogForWorkload(t *testing.T) {
	actions := CatalogFor("Deployment")
	var restart, servicePF, podExec, commonDelete bool
	for _, action := range actions {
		if action.Name == "restart" && action.Destructive {
			restart = true
		}
	}
	for _, action := range CatalogFor("Service") {
		if action.Name == "port-forward" {
			servicePF = true
		}
	}
	for _, action := range CatalogFor("Pod") {
		if action.Name == "exec" {
			podExec = true
		}
	}
	for _, action := range CatalogFor("ConfigMap") {
		if action.Name == "delete" {
			commonDelete = true
		}
	}
	if !restart || !servicePF || !podExec || !commonDelete {
		t.Fatalf("unexpected catalog coverage restart=%v servicePF=%v podExec=%v commonDelete=%v", restart, servicePF, podExec, commonDelete)
	}
	for kind, expected := range map[string][]string{
		"DaemonSet": {"restart", "logs"}, "Job": {"suspend", "resume", "logs"}, "CronJob": {"suspend", "resume"},
	} {
		catalog := CatalogFor(kind)
		for _, name := range expected {
			var found bool
			for _, action := range catalog {
				found = found || action.Name == name
			}
			if !found {
				t.Fatalf("%s catalog missing %s: %#v", kind, name, catalog)
			}
		}
	}
}

func TestDefaultEditorUsesEnv(t *testing.T) {
	t.Setenv("EDITOR", "nano")
	editor := DefaultEditor()
	if editor.Command != "nano" {
		t.Fatalf("expected EDITOR command, got %#v", editor)
	}
}

type editFunc func(string) error

func (f editFunc) Edit(path string) error { return f(path) }

func TestEditResourceAppliesEditorPatch(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "settings",
			"namespace": "default",
		},
		"data": map[string]any{"mode": "old"},
	}}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj)
	updated, err := EditResource(context.Background(), client, gvr, obj, editFunc(func(path string) error {
		return os.WriteFile(path, []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n  namespace: default\ndata:\n  mode: new\n"), 0o600)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ := unstructured.NestedString(updated.Object, "data", "mode"); got != "new" {
		t.Fatalf("expected edited data, got %#v", updated.Object)
	}
}

func TestEditResourceNoopWhenUnchanged(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "settings",
			"namespace": "default",
		},
		"data": map[string]any{"mode": "old"},
	}}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj)
	updated, err := EditResource(context.Background(), client, gvr, obj, editFunc(func(string) error {
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if updated.GetName() != "settings" {
		t.Fatalf("unexpected updated object %#v", updated)
	}
}

func TestEditPlanUsesUIDVersionAndServerDryRun(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "settings", "namespace": "default", "uid": "uid-1", "resourceVersion": "7"},
		"data":     map[string]any{"mode": "old"},
	}}
	plan, err := PrepareEdit(obj, editFunc(func(path string) error {
		return os.WriteFile(path, []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n  namespace: default\n  uid: uid-1\n  resourceVersion: \"7\"\ndata:\n  mode: new\n"), 0o600)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plan.Patch), `"uid":"uid-1"`) || !strings.Contains(string(plan.Patch), `"resourceVersion":"7"`) {
		t.Fatalf("edit patch is not identity-bound: %s", plan.Patch)
	}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj)
	var sawDryRun bool
	client.PrependReactor("patch", "configmaps", func(action ktesting.Action) (bool, runtime.Object, error) {
		patchAction := action.(ktesting.PatchActionImpl)
		dryRun := patchAction.GetPatchOptions().DryRun
		sawDryRun = len(dryRun) == 1 && dryRun[0] == metav1.DryRunAll
		return true, obj, nil
	})
	if err := PreviewEditPlan(context.Background(), client, gvr, plan); err != nil {
		t.Fatal(err)
	}
	if !sawDryRun {
		t.Fatal("expected edit preview to use server dry-run")
	}
}

func TestEditResourceConflictReturnsDiff(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "settings",
			"namespace": "default",
		},
		"data": map[string]any{"mode": "old"},
	}}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj)
	client.PrependReactor("patch", "configmaps", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, "settings", errTestConflict)
	})
	_, err := EditResource(context.Background(), client, gvr, obj, editFunc(func(path string) error {
		return os.WriteFile(path, []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n  namespace: default\ndata:\n  mode: new\n"), 0o600)
	}))
	conflict, ok := err.(*EditConflictError)
	if !ok {
		t.Fatalf("expected conflict error, got %T %v", err, err)
	}
	if !strings.Contains(conflict.Diff, "-   mode: old") || !strings.Contains(conflict.Diff, "+   mode: new") {
		t.Fatalf("unexpected diff:\n%s", conflict.Diff)
	}
}

func TestPatchResource(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "settings",
			"namespace": "default",
		},
		"data": map[string]any{"mode": "old"},
	}}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj)
	patched, err := PatchResource(context.Background(), client, gvr, "default", "settings", []byte(`{"data":{"mode":"new"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ := unstructured.NestedString(patched.Object, "data", "mode"); got != "new" {
		data, _ := json.Marshal(patched.Object)
		t.Fatalf("unexpected object %s", data)
	}
}

func TestClusterScopedPatchAndDeleteResource(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name": "demo",
		},
	}}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj)
	if _, err := PatchResource(context.Background(), client, gvr, "", "demo", []byte(`{"metadata":{"labels":{"env":"test"}}}`)); err != nil {
		t.Fatal(err)
	}
	if err := DeleteResource(context.Background(), client, gvr, "", "demo"); err != nil {
		t.Fatal(err)
	}
}

func TestStrategicMergePatch(t *testing.T) {
	patch, err := StrategicMergePatch(
		[]byte("apiVersion: v1\nkind: ConfigMap\ndata:\n  mode: old\n"),
		[]byte("apiVersion: v1\nkind: ConfigMap\ndata:\n  mode: new\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(patch) != `{"data":{"mode":"new"}}` {
		t.Fatalf("unexpected patch %s", patch)
	}
}

func TestStrategicMergePatchCarriesResourceVersion(t *testing.T) {
	patch, err := StrategicMergePatch(
		[]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n  resourceVersion: \"42\"\ndata:\n  mode: old\n"),
		[]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n  resourceVersion: \"42\"\ndata:\n  mode: new\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patch), `"resourceVersion":"42"`) {
		t.Fatalf("missing concurrency precondition in %s", patch)
	}
}
