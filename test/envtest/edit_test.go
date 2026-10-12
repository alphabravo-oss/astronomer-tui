package envtest

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/actions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

type fixtureEditor func(string) error

func (edit fixtureEditor) Edit(path string) error { return edit(path) }

func TestEnvtestEditDeletionAndConcurrentConflict(t *testing.T) {
	cfg, stop := startEnvtest(t)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	typed, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ns := createNamespace(ctx, t, typed, "astrotui-edit")
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	api := dyn.Resource(gvr).Namespace(ns)
	obj, err := api.Create(ctx, &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "settings", "namespace": ns}, "data": map[string]any{"keep": "yes", "remove": "old"}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	editor := fixtureEditor(func(path string) error {
		edited := obj.DeepCopy()
		unstructured.RemoveNestedField(edited.Object, "data", "remove")
		data, err := actions.YAMLForEdit(edited)
		if err != nil {
			return err
		}
		return os.WriteFile(path, data, 0600)
	})
	updated, err := actions.EditResource(ctx, dyn, gvr, obj, editor)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, _ := unstructured.NestedString(updated.Object, "data", "remove"); found {
		t.Fatal("API retained deleted field")
	}
	// A stale editor must not silently overwrite a concurrent change.
	_, err = actions.EditResource(ctx, dyn, gvr, obj, editor)
	var conflict *actions.EditConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected version conflict, got %v", err)
	}
}
