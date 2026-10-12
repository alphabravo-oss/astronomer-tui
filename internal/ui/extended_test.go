package ui

import (
	"context"
	"fmt"
	"github.com/alphabravo-oss/astrotui/internal/ai"
	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/config"
	"github.com/alphabravo-oss/astrotui/internal/logs"
	"github.com/alphabravo-oss/astrotui/internal/ui/views"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"io"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSlowOperationCanBeCancelledWithoutLateNavigation(t *testing.T) {
	app := qualityApp(t)
	screen := startQualityScreen(t, app, 80, 24)
	started := make(chan struct{})
	cancelled := make(chan struct{})
	app.app.QueueUpdateDraw(func() {
		app.operation("Slow API", func(ctx context.Context) (func(), error) {
			close(started)
			select {
			case <-ctx.Done():
				close(cancelled)
				return nil, ctx.Err()
			case <-time.After(5 * time.Second):
				return func() { app.showMessage("Wrong", "late result") }, nil
			}
		})
	})
	<-started
	start := time.Now()
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyEsc, 0, 0))
	if !waitForScreenText(t, app, screen, "Astro-TUI") {
		t.Fatal("input froze during API work")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("cancel input too slow")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("request not cancelled")
	}
}
func TestAIRequiresPreviewAndDoesNotExecuteSuggestions(t *testing.T) {
	app := qualityApp(t)
	app.options.AIProvider = ai.MockProvider{Response: `{"action":"delete"}`}
	screen := startQualityScreen(t, app, 100, 30)
	app.app.QueueUpdateDraw(func() {
		view := tview.NewTextView()
		app.streamAIResponse(view, ai.ManifestRequest("kind: ConfigMap\nmetadata:\n  name: example"))
	})
	if !waitForScreenText(t, app, screen, "Review AI request") {
		t.Fatal("missing outgoing preview")
	}
	if !waitForScreenText(t, app, screen, "ConfigMap") {
		t.Fatal("missing exact outgoing data")
	}
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyEsc, 0, 0))
}
func TestLogServerTimestampsSourcesAndWrappedRows(t *testing.T) {
	b := logs.NewBuffer(10)
	w := newLogLineWriter("api", b, nil)
	w.target = logs.PodLogTarget{Namespace: "payments", Pod: "api", Container: "worker"}
	_, _ = w.Write([]byte("2026-10-12T10:20:30.123Z " + strings.Repeat("x", 120) + "\n"))
	w.Flush()
	entries := b.Entries()
	if entries[0].Namespace != "payments" || entries[0].Container != "worker" || entries[0].Time.Nanosecond() != 123000000 {
		t.Fatal(entries)
	}
	view := tview.NewTextView()
	view.SetRect(0, 0, 40, 20)
	text, rows := logDocument(view, entries, true)
	if len(rows) < 3 || !strings.Contains(strings.ReplaceAll(text, "\n", ""), "payments/api/worker") {
		t.Fatal("missing source/wrap", text)
	}
	for _, entry := range rows {
		if entry.ID != entries[0].ID {
			t.Fatal("wrapped source identity lost")
		}
	}
}
func TestConfiguredViewAndMetrics(t *testing.T) {
	app := qualityApp(t)
	app.custom = config.Custom{Views: map[string]config.View{"v1/pods": {Columns: []string{"NAME", "TEAM:.metadata.labels.team", "COUNT:.spec.count|N", "AGE|W"}}}}
	obj := podObject("api", "default")
	obj.SetLabels(map[string]string{"team": "ops"})
	_ = unstructured.SetNestedField(obj.Object, int64(10), "spec", "count")
	r := app.configuredRenderer(app.renderer)
	row := r.Row(obj)
	if row[1].Text != "ops" || row[2].Text != "10" {
		t.Fatal(row)
	}
	if len(app.displayColumns(r)) != 3 {
		t.Fatal("wide column displayed")
	}
	base := views.Builtins()[app.currentGVR]
	metric := views.MetricsRenderer{Renderer: base, Now: time.Now(), Usage: map[string]cluster.Usage{"default/api": {CPUMilli: 125, MemoryBytes: 16 * 1024 * 1024, Timestamp: time.Now()}}}
	for i, c := range metric.Header() {
		if c.Name == "CPU" && metric.Row(obj)[i].Text != "125m" {
			t.Fatal("incorrect metric")
		}
	}
	metric.Error = fmt.Errorf("forbidden")
	for i, c := range metric.Header() {
		if c.Name == "CPU" && metric.Row(obj)[i].Text != "n/a" {
			t.Fatal("missing unavailable state")
		}
	}
}
func TestEventsUseUIDNotObjectName(t *testing.T) {
	app := qualityApp(t)
	pod := podObject("same", "default")
	pod.SetUID("selected")
	g := schema.GroupVersionResource{Version: "v1", Resource: "events"}
	app.options.Client.Dynamic = dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{g: "EventList"})
	app.cache = cluster.NewResourceCache(app.options.Client.Dynamic, 0)
	defer app.stopBackground()
	app.showObjectEvents(pod)
	event := func(id string) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{"involvedObject": map[string]any{"uid": id, "name": "same"}}}
	}
	if app.relatedPredicate == nil || app.relatedPredicate(event("replaced")) || !app.relatedPredicate(event("selected")) {
		t.Fatal("events match wrong identity")
	}

}

func TestRepeatedNamespaceSwitchesReleaseWatches(t *testing.T) {
	g := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), podObject("api", "default"), podObject("other", "payments"))
	var active atomic.Int64
	client.PrependWatchReactor("pods", func(action ktesting.Action) (bool, watch.Interface, error) {
		active.Add(1)
		return true, &countedWatch{Interface: watch.NewRaceFreeFake(), stop: func() { active.Add(-1) }}, nil
	})
	app := New(Options{Client: &cluster.Client{Context: "test", Namespace: "default", Dynamic: client}, ConfigDir: t.TempDir()})
	screen := tcell.NewSimulationScreen("UTF-8")
	app.SetScreen(screen)
	screen.SetSize(100, 30)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("app leaked")
		}
	}()
	if !waitForScreenText(t, app, screen, "api") {
		t.Fatal("initial resource missing")
	}
	for i := 0; i < 100; i++ {
		ns := "default"
		if i%2 == 0 {
			ns = "payments"
		}
		app.app.QueueUpdate(func() { app.switchNamespace(ns) })
	}
	if !waitForScreenText(t, app, screen, "api") {
		t.Fatal("switches lost view")
	}
	deadline := time.Now().Add(time.Second)
	for active.Load() > 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if active.Load() > 1 {
		t.Fatalf("%d active watches after switches", active.Load())
	}
	cancel()
	deadline = time.Now().Add(time.Second)
	for active.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if active.Load() != 0 {
		t.Fatal("watch survives shutdown")
	}
	_ = g
}

type countedWatch struct {
	watch.Interface
	once sync.Once
	stop func()
}

func (w *countedWatch) Stop() { w.once.Do(func() { w.Interface.Stop(); w.stop() }) }

func TestBusyLogInputRemainsResponsive(t *testing.T) {
	app := qualityApp(t)
	app.options.LogStreamer = func(ctx context.Context, target logs.PodLogTarget, options logs.RequestOptions, out io.Writer) error {
		for i := 0; i < 20000; i++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			_, _ = fmt.Fprintf(out, "2026-10-12T12:00:00Z entry-%d some running workload output\n", i)
		}
		<-ctx.Done()
		return ctx.Err()
	}
	screen := startQualityScreen(t, app, 120, 40)
	app.app.QueueUpdateDraw(func() { app.showLogsView([]logs.PodLogTarget{{Namespace: "default", Pod: "api", Container: "app"}}) })
	start := time.Now()
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	if !waitForScreenText(t, app, screen, "PAUSED") {
		t.Fatal("pause failed under log load")
	}
	if time.Since(start) > time.Second {
		t.Fatal("input stalled")
	}
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyEsc, 0, 0))
	if !waitForScreenText(t, app, screen, "Astro-TUI") {
		t.Fatal("exit failed under log load")
	}
}
