//go:build e2e

package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/config"
	"github.com/alphabravo-oss/astrotui/internal/session"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

func TestLiveParityExplorersAndBenchmark(t *testing.T) {
	if os.Getenv("ASTROTUI_E2E") != "1" {
		t.Skip("requires isolated Kubernetes cluster")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ns := fmt.Sprintf("astrotui-parity-%d", time.Now().UnixNano())
	c, err := cluster.NewClient(ctx, cluster.ClientOptions{Context: os.Getenv("ASTROTUI_E2E_CONTEXT"), Namespace: ns})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		clean, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = c.Clientset.CoreV1().Namespaces().Delete(clean, ns, metav1.DeleteOptions{})
	}()
	_, err = c.Clientset.CoreV1().ServiceAccounts(ns).Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "default"}}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	n := int32(1)
	_, err = c.Clientset.AppsV1().Deployments(ns).Create(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "parity-api"}, Spec: appsv1.DeploymentSpec{Replicas: &n, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "parity"}}, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "parity"}}, Spec: corev1.PodSpec{SchedulingGates: []corev1.PodSchedulingGate{{Name: "astrotui.test/fixture"}}, Containers: []corev1.Container{{Name: "main", Image: "registry.k8s.io/pause:3.9"}}}}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 15*time.Second, true, func(ctx context.Context) (bool, error) {
		p, err := c.Clientset.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
		return err == nil && len(p.Items) > 0, err
	}); err != nil {
		t.Fatal(err)
	}
	_, err = c.Clientset.RbacV1().Roles(ns).Create(ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "pod-reader"}, Rules: []rbacv1.PolicyRule{{Verbs: []string{"get", "list"}, APIGroups: []string{""}, Resources: []string{"pods"}}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Clientset.RbacV1().RoleBindings(ns).Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "reader"}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: "pod-reader"}, Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Namespace: ns, Name: "runner"}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	app := New(Options{Client: c, Config: config.Default(), ConfigDir: t.TempDir(), InitialState: session.State{LastView: "deployments"}})
	screen := tcell.NewSimulationScreen("UTF-8")
	app.SetScreen(screen)
	screen.SetSize(120, 36)
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("TUI failed to stop")
		}
	}()
	if !waitForScreenText(t, app, screen, "parity-api") {
		t.Fatal("initial deployment missing")
	}
	capture := func(name string) {
		if dir := os.Getenv("ASTROTUI_PARITY_SCREENSHOTS"); dir != "" {
			exportScreenSVG(t, app, screen, filepath.Join(dir, name+".svg"))
		}
	}
	app.app.QueueUpdateDraw(app.showPulses)
	if !waitForScreenText(t, app, screen, "WARN HISTORY") {
		t.Fatal("Pulses unreachable")
	}
	if !waitForScreenText(t, app, screen, "persistentvolumeclaims") {
		t.Fatal("Pulses never loaded")
	}
	capture("astrotui-pulses")
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyEsc, 0, 0))
	if !waitForScreenText(t, app, screen, "Astro-TUI") {
		t.Fatal("Pulses did not release view")
	}
	app.app.QueueUpdateDraw(func() { app.showXRay("deployments", ns) })
	if !waitForScreenText(t, app, screen, "containers/main") {
		t.Fatal("XRay did not traverse real deployment -> replica set -> pod")
	}
	capture("astrotui-xray")
	app.app.QueueUpdateDraw(func() { app.showRBAC([]string{"sa", "runner", ns}) })
	if !waitForScreenText(t, app, screen, "pod-reader") {
		t.Fatal("RBAC grant missing")
	}
	app.app.QueueUpdateDraw(func() { app.showWhoCan([]string{"get", "pods", ns}) })
	if !waitForScreenText(t, app, screen, "ServiceAccount/"+ns+"/runner") {
		t.Fatal("RBAC reverse lookup missing")
	}
	capture("astrotui-rbac")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer server.Close()
	app.app.QueueUpdateDraw(func() { app.showBenchmark(server.URL) })
	if !waitForScreenText(t, app, screen, "HTTP benchmark") {
		t.Fatal("benchmark form missing")
	}
	if calls.Load() != 0 {
		t.Fatal("opening form created traffic")
	}
	focused := false
	for i := 0; i < 8; i++ {
		app.app.QueueUpdateDraw(func() {
			if b, ok := app.app.GetFocus().(*tview.Button); ok && b.GetLabel() == "Review run" {
				focused = true
			}
		})
		if focused {
			break
		}
		app.app.QueueEvent(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	}
	if !focused {
		t.Fatal("benchmark review button not keyboard reachable")
	}
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if !waitForScreenText(t, app, screen, "real HTTP traffic") {
		t.Fatal("benchmark confirmation missing")
	}
	if calls.Load() != 0 {
		t.Fatal("review created traffic")
	}
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if !waitForScreenText(t, app, screen, "Benchmark report") {
		t.Fatalf("benchmark did not finish:\n%s", captureScreen(app, screen))
	}
	capture("astrotui-benchmark")
	if calls.Load() != 200 {
		t.Fatalf("request budget: %d", calls.Load())
	}
	app.app.QueueUpdateDraw(func() { app.showArchives("benchmarks") })
	if !waitForScreenText(t, app, screen, "benchmark-") {
		t.Fatal("saved report missing")
	}
}
