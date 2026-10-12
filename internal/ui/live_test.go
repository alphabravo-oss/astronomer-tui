//go:build e2e

package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/config"
	"github.com/alphabravo-oss/astrotui/internal/session"
	"github.com/gdamore/tcell/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestLiveTUIResourceWorkflow(t *testing.T) {
	if os.Getenv("ASTROTUI_E2E") != "1" {
		t.Skip("set ASTROTUI_E2E=1 with an isolated test cluster")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	namespace := fmt.Sprintf("astrotui-ui-%d", time.Now().UnixNano())
	client, err := cluster.NewClient(ctx, cluster.ClientOptions{Context: os.Getenv("ASTROTUI_E2E_CONTEXT"), Namespace: namespace})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = client.Clientset.CoreV1().Namespaces().Delete(cleanup, namespace, metav1.DeleteOptions{})
	}()
	create := func(name string) {
		t.Helper()
		if _, err := client.Clientset.CoreV1().ConfigMaps(namespace).Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name}}, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	create("state-one")
	app := New(Options{Client: client, Config: config.Default(), InitialState: session.State{LastView: "configmaps"}})
	screen := tcell.NewSimulationScreen("UTF-8")
	app.SetScreen(screen)
	screen.SetSize(120, 30)
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
			t.Error("live TUI did not exit")
		}
	}()
	if !waitForScreenText(t, app, screen, "state-one") {
		t.Fatal("live initial resource missing")
	}
	create("state-two")
	if !waitForScreenText(t, app, screen, "state-two") {
		t.Fatal("live watch did not update screen")
	}
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyRune, '/', 0))
	for _, r := range "state-two" {
		app.app.QueueEvent(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	if !waitForScreenText(t, app, screen, "configmaps [1/") {
		t.Fatal("live filter did not narrow rows")
	}
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyEsc, 0, 0))
	if !waitForScreenText(t, app, screen, "state-one") {
		t.Fatal("cancel did not restore rows")
	}
	app.app.QueueUpdateDraw(func() { app.switchNamespace("default") })
	if !waitForScreenText(t, app, screen, "Namespace: default") {
		t.Fatal("live namespace did not switch")
	}
	app.app.QueueUpdate(func() {
		for _, obj := range app.visibleObjects {
			if obj.GetNamespace() != "default" {
				t.Error("stale namespace row displayed")
			}
		}
	})
}

func TestLiveWatchToVisibleLatency(t *testing.T) {
	if os.Getenv("ASTROTUI_E2E") != "1" {
		t.Skip("requires isolated API server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ns := fmt.Sprintf("astrotui-latency-%d", time.Now().UnixNano())
	client, err := cluster.NewClient(ctx, cluster.ClientOptions{Context: os.Getenv("ASTROTUI_E2E_CONTEXT"), Namespace: ns})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = client.Clientset.CoreV1().Namespaces().Delete(cleanup, ns, metav1.DeleteOptions{})
	}()
	obj, err := client.Clientset.CoreV1().ConfigMaps(ns).Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "latency"}, Data: map[string]string{"state": "marker-0"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err = os.WriteFile(filepath.Join(dir, "views.yaml"), []byte("views:\n  v1/configmaps:\n    columns: [NAME, 'STATE:.data.state']\n"), 0600); err != nil {
		t.Fatal(err)
	}
	app := New(Options{Client: client, Config: config.Default(), ConfigDir: dir, InitialState: session.State{LastView: "configmaps"}})
	screen := tcell.NewSimulationScreen("UTF-8")
	app.SetScreen(screen)
	screen.SetSize(120, 40)
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("UI shutdown timed out")
		}
	}()
	if !waitForScreenText(t, app, screen, "marker-0") {
		t.Fatal("custom view did not initialize")
	}
	var samples []float64
	for i := 1; i <= 20; i++ {
		marker := fmt.Sprintf("marker-%03d", i)
		obj.Data["state"] = marker
		start := time.Now()
		obj, err = client.Clientset.CoreV1().ConfigMaps(ns).Update(ctx, obj, metav1.UpdateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !waitForScreenText(t, app, screen, marker) {
			t.Fatal("watch update not painted")
		}
		samples = append(samples, float64(time.Since(start).Microseconds())/1000)
	}
	ordered := append([]float64(nil), samples...)
	sort.Float64s(ordered)
	p95 := ordered[18]
	t.Logf("API mutation to visible frame p95 %.2f ms (%d samples)", p95, len(samples))
	if path := os.Getenv("ASTROTUI_WATCH_METRICS"); path != "" {
		body, _ := json.MarshalIndent(map[string]any{"measurement": "API mutation through informer to tcell simulation frame", "samples_ms": samples, "p95_ms": p95}, "", "  ")
		if err = os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if p95 > 500 {
		t.Fatalf("watch-to-frame p95 %.2f ms exceeds 500 ms", p95)
	}
}
