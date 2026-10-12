package actions

import (
	"context"
	"fmt"
	"github.com/alphabravo-oss/astrotui/internal/config"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"strings"
	"testing"
	"time"
)

func TestAdministrativeActionsPoliciesAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		kind, resource, group, action, path string
		value                               any
	}{{"DaemonSet", "daemonsets", "apps", "restart", "", nil}, {"ReplicaSet", "replicasets", "apps", "scale", "replicas", int64(3)}, {"Node", "nodes", "", "cordon", "unschedulable", true}, {"Node", "nodes", "", "uncordon", "unschedulable", false}, {"CronJob", "cronjobs", "batch", "suspend", "suspend", true}, {"Job", "jobs", "batch", "resume", "suspend", false}} {
		t.Run(tc.action+tc.kind, func(t *testing.T) {
			g := schema.GroupVersionResource{Group: tc.group, Version: "v1", Resource: tc.resource}
			obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": g.GroupVersion().String(), "kind": tc.kind, "metadata": map[string]any{"name": "target", "uid": "original", "resourceVersion": "1"}, "spec": map[string]any{}}}
			dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj)
			e := Executor{Dynamic: dyn, Config: config.Default(), CurrentContext: "test"}
			r := Request{Name: tc.action, Kind: tc.kind, GVR: g, ResourceName: "target", UID: "original", Args: map[string]string{"replicas": "3"}}
			if _, err := e.Run(context.Background(), r); err != nil {
				t.Fatal(err)
			}
			updated, err := dyn.Resource(g).Get(context.Background(), "target", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if tc.path != "" {
				value, _, _ := unstructured.NestedFieldNoCopy(updated.Object, "spec", tc.path)
				if value != tc.value {
					t.Fatalf("%v != %v", value, tc.value)
				}
			} else {
				value, _, _ := unstructured.NestedString(updated.Object, "spec", "template", "metadata", "annotations", "kubectl.kubernetes.io/restartedAt")
				if value == "" {
					t.Fatal("restart annotation missing")
				}
			}
			r.UID = "replaced"
			if _, err = e.Run(context.Background(), r); err == nil {
				t.Fatal("replacement mutated")
			}
			e.Config.Contexts = map[string]config.ContextConfig{"test": {Policy: "read-only"}}
			r.UID = "original"
			before := len(dyn.Actions())
			if _, err = e.Run(context.Background(), r); err == nil {
				t.Fatal("read-only allowed")
			}
			if len(dyn.Actions()) != before {
				t.Fatal("read-only called API")
			}
			e.Config.Contexts["test"] = config.ContextConfig{Policy: "confirm-destructive"}
			if _, err = e.Run(context.Background(), r); err == nil {
				t.Fatal("production allowed without confirmation")
			}
			r.ConfirmText = "test"
			if _, err = e.Run(context.Background(), r); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestTriggerCronJobCopiesTemplateAndVerifiesUID(t *testing.T) {
	cron := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "default", UID: "cron-uid"}, Spec: batchv1.CronJobSpec{JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "worker", Image: "test"}}}}}}}}
	client := kubefake.NewSimpleClientset(cron)
	e := Executor{Clientset: client}
	r := Request{Name: "trigger-job", Kind: "CronJob", Namespace: "default", ResourceName: "nightly", UID: "cron-uid"}
	if _, err := e.Run(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	list, err := client.BatchV1().Jobs("default").List(context.Background(), metav1.ListOptions{})
	if err != nil || len(list.Items) != 1 || list.Items[0].Spec.Template.Spec.Containers[0].Image != "test" {
		t.Fatal(list, err)
	}
	r.UID = "replacement"
	if _, err = e.Run(context.Background(), r); err == nil {
		t.Fatal("wrong CronJob triggered")
	}
}
func TestDrainPreflightEvictionAndCancellation(t *testing.T) {
	controller := true
	for _, mode := range []string{"unmanaged", "emptydir", "success", "pdb", "error"} {
		t.Run(mode, func(t *testing.T) {
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker", UID: "node-id"}}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", UID: "pod-id", OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "app", UID: "rs-id", Controller: &controller}}}, Spec: corev1.PodSpec{NodeName: "worker"}}
			if mode == "unmanaged" {
				pod.OwnerReferences = nil
			}
			if mode == "emptydir" {
				pod.Spec.Volumes = []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}
			}
			client := kubefake.NewSimpleClientset(node, pod)
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			dyn := dynamicfake.NewSimpleDynamicClient(scheme, node)
			client.PrependReactor("create", "pods", func(a ktesting.Action) (bool, runtime.Object, error) {
				ev := a.(ktesting.CreateAction).GetObject().(*policyv1.Eviction)
				if ev.DeleteOptions.Preconditions == nil || *ev.DeleteOptions.Preconditions.UID != "pod-id" {
					t.Error("eviction identity missing")
				}
				switch mode {
				case "pdb":
					return true, nil, apierrors.NewTooManyRequests("budget", 1)
				case "error":
					return true, nil, fmt.Errorf("eviction denied")
				}
				_ = client.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("pods"), "default", "app")
				return true, nil, nil
			})
			e := Executor{Clientset: client, Dynamic: dyn}
			r := Request{Name: "drain", Kind: "Node", GVR: corev1.SchemeGroupVersion.WithResource("nodes"), ResourceName: "worker", UID: "node-id"}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			_, err := e.Run(ctx, r)
			if mode == "success" && err != nil {
				t.Fatal(err)
			}
			if mode != "success" && err == nil {
				t.Fatal("unsafe drain allowed")
			}
			if mode == "pdb" && !strings.Contains(err.Error(), "remains cordoned") {
				t.Fatal(err)
			}
			updated, _ := dyn.Resource(r.GVR).Get(context.Background(), "worker", metav1.GetOptions{})
			cordoned, _, _ := unstructured.NestedBool(updated.Object, "spec", "unschedulable")
			if (mode == "unmanaged" || mode == "emptydir") && cordoned {
				t.Fatal("preflight changed node")
			}
		})
	}
	_ = appsv1.SchemeGroupVersion
}

func TestNamedServiceForwardPrefersRunningPod(t *testing.T) {
	client := kubefake.NewSimpleClientset(&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"}, Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "api"}, Ports: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromString("http")}}}}, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "aaa-pending", Namespace: "default", Labels: map[string]string{"app": "api"}}}, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "zzz-running", Namespace: "default", Labels: map[string]string{"app": "api"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}}}}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}})
	service, _ := client.CoreV1().Services("default").Get(context.Background(), "api", metav1.GetOptions{})
	spec := DefaultServiceForwards(*service)[0]
	resolved, err := ResolvePortForwardSpec(context.Background(), client, spec)
	if err != nil || resolved.Resource != "pod/zzz-running" || resolved.Remote != 8080 {
		t.Fatal(resolved, err)
	}
	spec.TargetPortName = "missing"
	if _, err = ResolvePortForwardSpec(context.Background(), client, spec); err == nil {
		t.Fatal("missing named target accepted")
	}
}
