//go:build e2e

package kind

import (
	"context"
	"fmt"
	"github.com/alphabravo-oss/astrotui/internal/actions"
	"github.com/alphabravo-oss/astrotui/internal/cluster"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"os"
	"testing"
	"time"
)

func TestAdministrativeWorkflowsAgainstAPI(t *testing.T) {
	requireKindE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := cluster.NewClient(ctx, cluster.ClientOptions{Context: os.Getenv("ASTROTUI_E2E_CONTEXT")})
	if err != nil {
		t.Fatal(err)
	}
	ns := fmt.Sprintf("astrotui-admin-%d", time.Now().UnixNano())
	_, err = client.Clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = client.Clientset.CoreV1().Namespaces().Delete(cleanup, ns, metav1.DeleteOptions{})
	}()
	template := corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "admin-fixture"}}, Spec: corev1.PodSpec{NodeSelector: map[string]string{"astrotui.test/absent": "true"}, Containers: []corev1.Container{{Name: "main", Image: "registry.k8s.io/pause:3.9"}}}}
	selector := &metav1.LabelSelector{MatchLabels: template.Labels}
	zero := int32(0)
	rs, err := client.Clientset.AppsV1().ReplicaSets(ns).Create(ctx, &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "scale", Namespace: ns}, Spec: appsv1.ReplicaSetSpec{Replicas: &zero, Selector: selector, Template: template}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ds, err := client.Clientset.AppsV1().DaemonSets(ns).Create(ctx, &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "restart", Namespace: ns}, Spec: appsv1.DaemonSetSpec{Selector: selector, Template: template}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jobTemplate := *template.DeepCopy()
	jobTemplate.Spec.RestartPolicy = corev1.RestartPolicyNever
	suspend := true
	cron, err := client.Clientset.BatchV1().CronJobs(ns).Create(ctx, &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "manual", Namespace: ns}, Spec: batchv1.CronJobSpec{Schedule: "0 0 * * *", Suspend: &suspend, JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{Template: jobTemplate}}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	e := actions.Executor{Clientset: client.Clientset, Dynamic: client.Dynamic, CurrentContext: "test"}
	run := func(name, kind, group, resource, target string, uid types.UID, args map[string]string) {
		t.Helper()
		_, err := e.Run(ctx, actions.Request{Name: name, Kind: kind, GVR: schema.GroupVersionResource{Group: group, Version: "v1", Resource: resource}, Namespace: ns, ResourceName: target, UID: uid, Args: args})
		if err != nil {
			t.Fatal(name, err)
		}
	}
	run("scale", "ReplicaSet", "apps", "replicasets", rs.Name, rs.UID, map[string]string{"replicas": "2"})
	updatedRS, err := client.Clientset.AppsV1().ReplicaSets(ns).Get(ctx, rs.Name, metav1.GetOptions{})
	if err != nil || *updatedRS.Spec.Replicas != 2 {
		t.Fatal("ReplicaSet did not scale", err)
	}
	run("restart", "DaemonSet", "apps", "daemonsets", ds.Name, ds.UID, nil)
	updatedDS, err := client.Clientset.AppsV1().DaemonSets(ns).Get(ctx, ds.Name, metav1.GetOptions{})
	if err != nil || updatedDS.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] == "" {
		t.Fatal("DaemonSet did not restart", err)
	}
	run("trigger-job", "CronJob", "batch", "cronjobs", cron.Name, cron.UID, nil)
	jobs, err := client.Clientset.BatchV1().Jobs(ns).List(ctx, metav1.ListOptions{})
	if err != nil || len(jobs.Items) != 1 {
		t.Fatal("Job not created", err)
	}
	job := jobs.Items[0]
	run("suspend", "Job", "batch", "jobs", job.Name, job.UID, nil)
	run("resume", "Job", "batch", "jobs", job.Name, job.UID, nil)
	run("resume", "CronJob", "batch", "cronjobs", cron.Name, cron.UID, nil)
	run("suspend", "CronJob", "batch", "cronjobs", cron.Name, cron.UID, nil)
	// An empty synthetic Node exercises API mutations without touching cluster workers.
	node, err := client.Clientset.CoreV1().Nodes().Create(ctx, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = client.Clientset.CoreV1().Nodes().Delete(cleanup, node.Name, metav1.DeleteOptions{})
	}()
	for _, name := range []string{"cordon", "uncordon", "drain"} {
		if _, err = e.Run(ctx, actions.Request{Name: name, Kind: "Node", GVR: schema.GroupVersionResource{Version: "v1", Resource: "nodes"}, ResourceName: node.Name, UID: node.UID}); err != nil {
			t.Fatal(name, err)
		}
	}
}
