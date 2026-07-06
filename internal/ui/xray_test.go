package ui

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func owned(kind, name, ownerUID string, extra map[string]any) *unstructured.Unstructured {
	obj := map[string]any{
		"kind":     kind,
		"metadata": map[string]any{"name": name, "namespace": "default"},
	}
	if ownerUID != "" {
		obj["metadata"].(map[string]any)["ownerReferences"] = []any{
			map[string]any{"uid": ownerUID, "name": "owner"},
		}
	}
	for k, v := range extra {
		obj[k] = v
	}
	u := &unstructured.Unstructured{Object: obj}
	return u
}

func withUID(u *unstructured.Unstructured, uid string) *unstructured.Unstructured {
	u.SetUID(types.UID(uid))
	return u
}

func TestXrayTreeDeployment(t *testing.T) {
	dep := withUID(owned("Deployment", "api", "", nil), "dep-uid")
	rs := withUID(owned("ReplicaSet", "api-abc", "dep-uid", nil), "rs-uid")
	pod := owned("Pod", "api-abc-1", "rs-uid", map[string]any{
		"spec":   map[string]any{"containers": []any{map[string]any{"name": "app"}}},
		"status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "app", "ready": true}}},
	})

	tree := xrayTree("Deployment",
		[]*unstructured.Unstructured{dep},
		[]*unstructured.Unstructured{rs},
		[]*unstructured.Unstructured{pod},
	)
	if len(tree) != 1 || tree[0].label != "Deployment/api" {
		t.Fatalf("unexpected root %#v", tree)
	}
	if len(tree[0].children) != 1 || tree[0].children[0].label != "ReplicaSet/api-abc" {
		t.Fatalf("unexpected rs level %#v", tree[0].children)
	}
	podNode := tree[0].children[0].children
	if len(podNode) != 1 || podNode[0].label != "Pod/api-abc-1" {
		t.Fatalf("unexpected pod level %#v", podNode)
	}
	container := podNode[0].children
	if len(container) != 1 || container[0].label != "Container/app" || container[0].color != "ok" {
		t.Fatalf("unexpected container level %#v", container)
	}
}

func TestXrayTreeStatefulSetDirectPods(t *testing.T) {
	sts := withUID(owned("StatefulSet", "db", "", nil), "sts-uid")
	pod := owned("Pod", "db-0", "sts-uid", map[string]any{
		"spec": map[string]any{"containers": []any{map[string]any{"name": "db"}}},
	})
	tree := xrayTree("StatefulSet",
		[]*unstructured.Unstructured{sts},
		nil,
		[]*unstructured.Unstructured{pod},
	)
	if len(tree) != 1 || len(tree[0].children) != 1 || tree[0].children[0].label != "Pod/db-0" {
		t.Fatalf("expected sts->pod directly, got %#v", tree)
	}
}

func TestXrayKindParsing(t *testing.T) {
	for _, alias := range []string{"deploy", "deployment", "dp"} {
		if xrayKind(alias) != "Deployment" {
			t.Fatalf("%q should map to Deployment", alias)
		}
	}
	if xrayKind("pods") != "" {
		t.Fatal("pods is not an xray root")
	}
}
