package actions

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/alphabravo-oss/astrotui/internal/config"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestMergePatchPreservesDeletesAndTypes(t *testing.T) {
	patch, err := StrategicMergePatch([]byte(`{"data":{"remove":"old","keep":"ok"},"value":1,"list":[1,2]}`), []byte(`{"data":{"keep":"ok"},"value":"1","list":[2]}`))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(patch, &got); err != nil {
		t.Fatal(err)
	}
	data := got["data"].(map[string]any)
	if value, exists := data["remove"]; !exists || value != nil {
		t.Fatalf("deleted key missing from patch: %s", patch)
	}
	if got["value"] != "1" || len(got["list"].([]any)) != 1 {
		t.Fatalf("type/array changes lost: %s", patch)
	}
}

func TestEditVersionPreconditionAndDeletion(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "settings", "namespace": "default", "resourceVersion": "42"},
		"data": map[string]any{"remove": "yes", "keep": "value"},
	}}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj)
	var captured map[string]any
	client.PrependReactor("patch", "configmaps", func(action ktesting.Action) (bool, runtime.Object, error) {
		if err := json.Unmarshal(action.(ktesting.PatchAction).GetPatch(), &captured); err != nil {
			t.Fatal(err)
		}
		return false, nil, nil
	})
	updated, err := EditResource(context.Background(), client, gvr, obj, editFunc(func(path string) error {
		return os.WriteFile(path, []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n  namespace: default\n  resourceVersion: '999'\ndata:\n  keep: value\n"), 0600)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if captured["metadata"].(map[string]any)["resourceVersion"] != "42" {
		t.Fatalf("missing original version: %#v", captured)
	}
	if _, exists, _ := unstructured.NestedString(updated.Object, "data", "remove"); exists {
		t.Fatal("field was not deleted")
	}
}

func TestEditRejectsIdentityChangeBeforeAPI(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "one"}}}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj)
	_, err := EditResource(context.Background(), client, schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, obj, editFunc(func(path string) error {
		return os.WriteFile(path, []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: two\n"), 0600)
	}))
	if err == nil || len(client.Actions()) != 0 {
		t.Fatal("identity edit reached API")
	}
}

func TestNegativeScaleRejectedWithoutAPI(t *testing.T) {
	client := kubefake.NewSimpleClientset()
	_, err := (Executor{Clientset: client, Config: config.Default()}).Run(context.Background(), Request{Name: "scale", Kind: "Deployment", Args: map[string]string{"replicas": "-1"}})
	if err == nil || len(client.Actions()) != 0 {
		t.Fatal("negative replicas reached API")
	}
}

func TestDeleteUsesSelectedUIDPrecondition(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	client.PrependReactor("delete", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		opts := action.(ktesting.DeleteAction).GetDeleteOptions()
		if opts.Preconditions == nil || opts.Preconditions.UID == nil || *opts.Preconditions.UID != "selected-uid" {
			t.Error("deletion does not protect against object replacement")
		}
		return true, nil, nil
	})
	_, err := (Executor{Dynamic: client, Config: config.Default()}).Run(context.Background(), Request{Name: "delete", ResourceName: "api", Namespace: "default", UID: "selected-uid", GVR: schema.GroupVersionResource{Version: "v1", Resource: "pods"}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestForceDeleteRetainsUIDAndZeroGrace(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	called := false
	client.PrependReactor("delete", "pods", func(a ktesting.Action) (bool, runtime.Object, error) {
		called = true
		opts := a.(ktesting.DeleteAction).GetDeleteOptions()
		if opts.Preconditions == nil || *opts.Preconditions.UID != "selected" || opts.GracePeriodSeconds == nil || *opts.GracePeriodSeconds != 0 {
			t.Fatalf("force options: %+v", opts)
		}
		return true, nil, nil
	})
	if err := deleteResourceOptions(context.Background(), client, schema.GroupVersionResource{Version: "v1", Resource: "pods"}, "default", "one", "selected", true); err != nil || !called {
		t.Fatalf("force delete: %v", err)
	}
}
