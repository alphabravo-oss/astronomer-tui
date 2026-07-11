package actions

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/alphabravo-oss/astrotui/internal/config"
)

func TestExecutorDeleteUsesSafetyGuard(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "settings",
			"namespace": "default",
		},
	}}
	executor := Executor{
		Dynamic:        dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj),
		Config:         config.Default(),
		CurrentContext: "prod-east",
	}
	_, err := executor.Run(context.Background(), Request{Name: "delete", Kind: "ConfigMap", Namespace: "default", ResourceName: "settings", GVR: gvr})
	if err == nil {
		t.Fatal("expected safety confirmation error")
	}
	result, err := executor.Run(context.Background(), Request{Name: "delete", Kind: "ConfigMap", Namespace: "default", ResourceName: "settings", GVR: gvr, ConfirmText: "prod-east"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Message != "deleted ConfigMap/settings" {
		t.Fatalf("unexpected result %#v", result)
	}
}

func TestExecutorScaleDeployment(t *testing.T) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr(int32(1))},
	}
	executor := Executor{
		Clientset:      kubefake.NewSimpleClientset(deployment),
		Config:         config.Default(),
		CurrentContext: "dev",
	}
	result, err := executor.Run(context.Background(), Request{Name: "scale", Kind: "Deployment", Namespace: "default", ResourceName: "api", Args: map[string]string{"replicas": "3"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Message != "scaled Deployment/api to 3" {
		t.Fatalf("unexpected result %#v", result)
	}
	got, err := executor.Clientset.AppsV1().Deployments("default").Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 3 {
		t.Fatalf("expected 3 replicas, got %#v", got.Spec.Replicas)
	}
}

func TestExecutorScaleStatefulSet(t *testing.T) {
	statefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "default"},
		Spec:       appsv1.StatefulSetSpec{Replicas: ptr(int32(1))},
	}
	executor := Executor{
		Clientset:      kubefake.NewSimpleClientset(statefulSet),
		Config:         config.Default(),
		CurrentContext: "dev",
	}
	result, err := executor.Run(context.Background(), Request{Name: "scale", Kind: "StatefulSet", Namespace: "default", ResourceName: "db", Args: map[string]string{"replicas": "2"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Message != "scaled StatefulSet/db to 2" {
		t.Fatalf("unexpected result %#v", result)
	}
	got, err := executor.Clientset.AppsV1().StatefulSets("default").Get(context.Background(), "db", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 2 {
		t.Fatalf("expected 2 replicas, got %#v", got.Spec.Replicas)
	}
}

func TestExecutorRestartDeployment(t *testing.T) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr(int32(1))},
	}
	executor := Executor{
		Clientset:      kubefake.NewSimpleClientset(deployment),
		Config:         config.Default(),
		CurrentContext: "dev",
	}
	result, err := executor.Run(context.Background(), Request{Name: "restart", Kind: "Deployment", Namespace: "default", ResourceName: "api"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Message != "restarted Deployment/api" {
		t.Fatalf("unexpected result %#v", result)
	}
	got, err := executor.Clientset.AppsV1().Deployments("default").Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if DeploymentRestartAnnotation(*got) == "" {
		t.Fatalf("expected restart annotation on %#v", got.Spec.Template.Annotations)
	}
}

func TestExecutorRestartStatefulSet(t *testing.T) {
	statefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "default"},
		Spec:       appsv1.StatefulSetSpec{Replicas: ptr(int32(1))},
	}
	executor := Executor{
		Clientset:      kubefake.NewSimpleClientset(statefulSet),
		Config:         config.Default(),
		CurrentContext: "dev",
	}
	result, err := executor.Run(context.Background(), Request{Name: "restart", Kind: "StatefulSet", Namespace: "default", ResourceName: "db"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Message != "restarted StatefulSet/db" {
		t.Fatalf("unexpected result %#v", result)
	}
	got, err := executor.Clientset.AppsV1().StatefulSets("default").Get(context.Background(), "db", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if StatefulSetRestartAnnotation(*got) == "" {
		t.Fatalf("expected restart annotation on %#v", got.Spec.Template.Annotations)
	}
}

func TestExecutorRejectsInvalidScaleAndUnknownAction(t *testing.T) {
	executor := Executor{Config: config.Default(), CurrentContext: "dev"}
	if _, err := executor.Run(context.Background(), Request{Name: "scale", Kind: "Deployment", Args: map[string]string{"replicas": "nope"}}); err == nil {
		t.Fatal("expected invalid scale error")
	}
	if _, err := executor.Run(context.Background(), Request{Name: "unknown"}); err == nil {
		t.Fatal("expected unknown action error")
	}
}

func TestExecutorRejectsChangedImmutableTarget(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	obj := namedUnstructured("ConfigMap", "settings", "default", "new-uid", "2")
	executor := Executor{
		Dynamic:        dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj),
		Config:         config.Default(),
		CurrentContext: "dev",
		RequireTarget:  true,
	}
	_, err := executor.Run(context.Background(), Request{Name: "delete", Target: Target{
		Context: "dev", GVR: gvr, Kind: "ConfigMap", Namespace: "default", Name: "settings", UID: "old-uid", ResourceVersion: "1",
	}})
	if err == nil || !strings.Contains(err.Error(), "action target changed") {
		t.Fatalf("expected immutable target rejection, got %v", err)
	}
}

func TestExecutorDeletePreviewUsesServerDryRun(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	obj := namedUnstructured("ConfigMap", "settings", "default", "uid-1", "1")
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj)
	var sawDryRun bool
	dynamicClient.PrependReactor("delete", "configmaps", func(action ktesting.Action) (bool, runtime.Object, error) {
		deleteAction := action.(ktesting.DeleteAction)
		dryRun := deleteAction.GetDeleteOptions().DryRun
		sawDryRun = len(dryRun) == 1 && dryRun[0] == metav1.DryRunAll
		return true, nil, nil
	})
	executor := Executor{Dynamic: dynamicClient, Config: config.Default(), CurrentContext: "dev", RequireTarget: true}
	preview, err := executor.Preview(context.Background(), Request{Name: "delete", Target: Target{
		Context: "dev", GVR: gvr, Kind: "ConfigMap", Namespace: "default", Name: "settings", UID: "uid-1", ResourceVersion: "1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !sawDryRun || !strings.Contains(preview.Summary, "Dry-run") {
		t.Fatalf("expected server dry-run preview, got %#v", preview)
	}
}

func TestExecutorSuspendsImmutableJob(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}
	obj := namedUnstructured("Job", "backup", "default", "uid-job", "4")
	obj.SetAPIVersion("batch/v1")
	obj.Object["spec"] = map[string]any{"suspend": false}
	executor := Executor{
		Dynamic: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj), Config: config.Default(),
		CurrentContext: "dev", RequireTarget: true,
	}
	result, err := executor.Run(context.Background(), Request{Name: "suspend", Target: Target{
		Context: "dev", GVR: gvr, Kind: "Job", Namespace: "default", Name: "backup", UID: "uid-job", ResourceVersion: "4",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Message != "suspended Job/backup" {
		t.Fatalf("unexpected result %#v", result)
	}
	updated, err := executor.Dynamic.Resource(gvr).Namespace("default").Get(context.Background(), "backup", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	suspended, _, _ := unstructured.NestedBool(updated.Object, "spec", "suspend")
	if !suspended {
		t.Fatalf("expected suspended job %#v", updated.Object)
	}
}

func namedUnstructured(kind, name, namespace string, uid types.UID, resourceVersion string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("v1")
	obj.SetKind(kind)
	obj.SetName(name)
	obj.SetNamespace(namespace)
	obj.SetUID(uid)
	obj.SetResourceVersion(resourceVersion)
	return obj
}

func ptr[T any](v T) *T {
	return &v
}
