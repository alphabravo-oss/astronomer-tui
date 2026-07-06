package actions

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"

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

func ptr[T any](v T) *T {
	return &v
}

func TestScaleReplicaSetAndRestartDaemonSet(t *testing.T) {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: "api-rs", Namespace: "default"},
		Spec:       appsv1.ReplicaSetSpec{Replicas: ptr(int32(1))},
	}
	ds := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "agent", Namespace: "default"}}
	exec := Executor{Clientset: kubefake.NewSimpleClientset(rs, ds), Config: config.Default(), CurrentContext: "dev"}

	if _, err := exec.Run(context.Background(), Request{Name: "scale", Kind: "ReplicaSet", Namespace: "default", ResourceName: "api-rs", Args: map[string]string{"replicas": "4"}}); err != nil {
		t.Fatal(err)
	}
	gotRS, _ := exec.Clientset.AppsV1().ReplicaSets("default").Get(context.Background(), "api-rs", metav1.GetOptions{})
	if gotRS.Spec.Replicas == nil || *gotRS.Spec.Replicas != 4 {
		t.Fatalf("expected 4 replicas, got %#v", gotRS.Spec.Replicas)
	}

	if _, err := exec.Run(context.Background(), Request{Name: "restart", Kind: "DaemonSet", Namespace: "default", ResourceName: "agent"}); err != nil {
		t.Fatal(err)
	}
	gotDS, _ := exec.Clientset.AppsV1().DaemonSets("default").Get(context.Background(), "agent", metav1.GetOptions{})
	if gotDS.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] == "" {
		t.Fatal("expected daemonset restart annotation")
	}
}

func TestCronJobTriggerAndSuspend(t *testing.T) {
	cj := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "report", Namespace: "default"},
		Spec: batchv1.CronJobSpec{
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "busybox"}}}}},
			},
		},
	}
	client := kubefake.NewSimpleClientset(cj)
	if err := TriggerCronJob(context.Background(), client, "default", "report", "report-manual-1"); err != nil {
		t.Fatal(err)
	}
	job, err := client.BatchV1().Jobs("default").Get(context.Background(), "report-manual-1", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if job.Labels["astrotui.io/triggered-by"] != "report" {
		t.Fatalf("expected triggered-by label, got %#v", job.Labels)
	}
	if len(job.Spec.Template.Spec.Containers) != 1 {
		t.Fatal("expected job template copied from cronjob")
	}
	if err := SuspendCronJob(context.Background(), client, "default", "report", true); err != nil {
		t.Fatal(err)
	}
	got, _ := client.BatchV1().CronJobs("default").Get(context.Background(), "report", metav1.GetOptions{})
	if got.Spec.Suspend == nil || !*got.Spec.Suspend {
		t.Fatalf("expected cronjob suspended, got %#v", got.Spec.Suspend)
	}
}
