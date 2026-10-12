package ui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/logs"
	"github.com/alphabravo-oss/astrotui/internal/session"
	"github.com/alphabravo-oss/astrotui/internal/ui/views"
	"github.com/gdamore/tcell/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func qualityApp(t *testing.T) *App {
	t.Helper()
	app := New(Options{Client: &cluster.Client{Context: "test", Namespace: "default"}})
	app.currentGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	app.renderer = views.GenericRenderer{Resource: app.currentGVR, Columns: []string{"NAME", "NAMESPACE", "RESTARTS", "AGE"}}
	return app
}

func startQualityScreen(t *testing.T, app *App, width, height int) tcell.SimulationScreen {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	app.SetScreen(screen)
	screen.SetSize(width, height)
	app.bindTableKeys(context.Background())
	app.showMain()
	done := make(chan error, 1)
	go func() { done <- app.app.Run() }()
	t.Cleanup(func() {
		app.stopBackground()
		app.app.Stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("terminal did not stop")
		}
	})
	if !waitForScreenText(t, app, screen, "Astro-TUI") {
		t.Fatal("main layout did not draw")
	}
	return screen
}

func TestSelectionSurvivesInsertionSortAndRemoval(t *testing.T) {
	app := qualityApp(t)
	beta, gamma := podObject("beta", "default"), podObject("gamma", "default")
	beta.SetUID(types.UID("beta-uid"))
	gamma.SetUID(types.UID("gamma-uid"))
	app.objects = []*unstructured.Unstructured{beta, gamma}
	app.renderCurrent()
	app.table.Select(2, 0)
	app.objects = append(app.objects, podObject("alpha", "default"))
	app.renderCurrent()
	if app.selectedObject().GetUID() != gamma.GetUID() {
		t.Fatal("insertion changed the action target")
	}
	app.setSort(0)
	if app.selectedObject().GetUID() != gamma.GetUID() {
		t.Fatal("sorting changed the action target")
	}
	app.objects = []*unstructured.Unstructured{beta}
	app.renderCurrent()
	if app.selectedObject() != beta {
		t.Fatal("removed selection did not clamp to a valid object")
	}
	app.objects = nil
	app.renderCurrent()
	if app.selectedObject() != nil {
		t.Fatal("empty view retained an action target")
	}
}

func TestNamespaceSwitchReloadsCachedScope(t *testing.T) {
	app := qualityApp(t)
	app.cache = cluster.NewResourceCache(dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), 0)
	index := app.cache.Ensure(app.currentGVR).GetIndexer()
	for _, ns := range []string{"default", "other"} {
		if err := index.Add(podObject("api", ns)); err != nil {
			t.Fatal(err)
		}
	}
	app.switchNamespace("other")
	if len(app.visibleObjects) != 1 || app.visibleObjects[0].GetNamespace() != "other" {
		t.Fatal("namespace switch retained stale rows")
	}
	app.switchNamespace("all")
	if len(app.visibleObjects) != 2 || app.options.Client.Namespace != "" {
		t.Fatal("all namespace view is not all namespaces")
	}
}

func TestResourceFilterModes(t *testing.T) {
	renderer := views.GenericRenderer{Columns: []string{"NAME", "NAMESPACE"}}
	api, worker := podObject("api-01", "default"), podObject("worker-01", "jobs")
	api.SetLabels(map[string]string{"app": "api", "tier": "web"})
	worker.SetLabels(map[string]string{"app": "worker"})
	for _, tc := range []struct {
		query string
		count int
	}{
		{"API", 1}, {"api|worker", 2}, {"!api", 1}, {"-l app=api,tier in (web,edge)", 1}, {"-f wrk", 1}, {"-t api|worker", 0}, {"", 2},
	} {
		t.Run(tc.query, func(t *testing.T) {
			rows, _ := BuildRows(renderer, []*unstructured.Unstructured{worker, api}, tc.query, 0, false)
			if len(rows) != tc.count {
				t.Fatalf("%q: got %d want %d", tc.query, len(rows), tc.count)
			}
		})
	}
	for _, bad := range []string{"[", "-l app in ("} {
		if _, err := compileResourceFilter(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestNumericAgeAndDeterministicSorting(t *testing.T) {
	app := qualityApp(t)
	a, b := podObject("a", "default"), podObject("b", "default")
	a.Object["status"].(map[string]any)["containerStatuses"] = []any{map[string]any{"restartCount": int64(10)}}
	b.Object["status"].(map[string]any)["containerStatuses"] = []any{map[string]any{"restartCount": int64(2)}}
	_, objects := BuildRows(app.renderer, []*unstructured.Unstructured{a, b}, "", 2, false)
	if objects[0] != b {
		t.Fatal("numeric column sorted lexically")
	}
	_, objects = BuildRows(app.renderer, []*unstructured.Unstructured{b, a}, "", 1, false)
	if objects[0] != a {
		t.Fatal("equal sort keys have unstable order")
	}
	newer, older := a.DeepCopy(), b.DeepCopy()
	newer.SetCreationTimestamp(metav1.NewTime(time.Now().Add(-time.Minute)))
	older.SetCreationTimestamp(metav1.NewTime(time.Now().Add(-time.Hour)))
	_, objects = BuildRows(app.renderer, []*unstructured.Unstructured{older, newer}, "", 3, false)
	if objects[0] != newer {
		t.Fatal("age sort does not use actual timestamps")
	}
}

func TestPaletteKeyboardAndLiteralQ(t *testing.T) {
	app := qualityApp(t)
	screen := startQualityScreen(t, app, 100, 30)
	app.app.QueueUpdateDraw(app.showPalette)
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyRune, 'q', 0))
	if !waitForScreenText(t, app, screen, "> q") {
		t.Fatal("q closed the palette")
	}
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyEsc, 0, 0))
	if !waitForScreenText(t, app, screen, "Astro-TUI") {
		t.Fatal("escape did not restore main view")
	}
	app.app.QueueUpdateDraw(app.showPalette)
	// Empty results begin with resources. Down must move while input retains focus.
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyRune, 'q', 0))
	if !waitForScreenText(t, app, screen, "> q") {
		t.Fatal("arrow navigation stole text input focus")
	}
}

func TestDeleteKeyboardCannotMutateWithoutConfirmation(t *testing.T) {
	app := qualityApp(t)
	obj := podObject("api", "default")
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj)
	app.options.Client.Dynamic = dyn
	app.objects = []*unstructured.Unstructured{obj}
	app.renderCurrent()
	screen := startQualityScreen(t, app, 100, 30)
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyRune, 'd', 0))
	if !waitForScreenText(t, app, screen, "kind: Pod") {
		t.Fatal("d did not open details")
	}
	if len(dyn.Actions()) != 0 {
		t.Fatal("describe sent an API mutation")
	}
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyEsc, 0, 0))
	if !waitForScreenText(t, app, screen, "Astro-TUI") {
		t.Fatal("details did not close")
	}
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyCtrlD, 0, 0))
	if !waitForScreenText(t, app, screen, "Delete Pod/api") {
		t.Fatalf("missing delete confirmation:\n%s", captureScreen(app, screen))
	}
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if !waitForScreenText(t, app, screen, "Astro-TUI") {
		t.Fatal("default confirmation did not cancel")
	}
	if len(dyn.Actions()) != 0 {
		t.Fatal("cancel sent an API mutation")
	}
}

func TestMainLayoutAtTerminalSizes(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 36}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			app := qualityApp(t)
			screen := startQualityScreen(t, app, size[0], size[1])
			text := captureScreen(app, screen)
			for _, want := range []string{"Context: test", "Namespace: default", "Ctrl+P palette", "No pods"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q at %v:\n%s", want, size, text)
				}
			}
		})
	}
}

func TestLongLogLinesAndFinalFlush(t *testing.T) {
	buffer := logs.NewBuffer(4)
	writer := newLogLineWriter("api", buffer, nil)
	line := strings.Repeat("x", 70_000)
	if _, err := writer.Write([]byte(line + "\nfinal")); err != nil {
		t.Fatal(err)
	}
	writer.Flush()
	entries := buffer.Entries()
	if len(entries) != 2 || entries[0].Line != line || entries[1].Line != "final" {
		t.Fatal("lost long or unterminated line")
	}
	_, _ = writer.Write([]byte(strings.Repeat("z", maxLogLineBytes*2)))
	writer.Flush()
	entries = buffer.Entries()
	if len(entries[2].Line) > maxLogLineBytes+32 || !strings.Contains(entries[2].Line, "truncated") {
		t.Fatal("unbounded long line")
	}
}

func TestCustomResourceAndAllNamespacesSession(t *testing.T) {
	app := qualityApp(t)
	app.currentGVR = schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}
	app.options.Client.Namespace = ""
	state := app.sessionState()
	if state.Namespace != "all" || initialResourceGVR(state) != app.currentGVR {
		t.Fatalf("session lost GVR/scope: %#v", state)
	}
	if initialResourceGVR(session.State{LastView: "pods"}).Resource != "pods" {
		t.Fatal("legacy session broken")
	}
}

func TestLiveFilterRejectsInvalidAndEscapeRestores(t *testing.T) {
	app := qualityApp(t)
	app.objects = []*unstructured.Unstructured{podObject("api", "default"), podObject("worker", "default")}
	app.renderCurrent()
	screen := startQualityScreen(t, app, 100, 30)
	app.app.QueueUpdateDraw(app.showFilter)
	for _, r := range "api" {
		app.app.QueueEvent(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	if !waitForScreenText(t, app, screen, "1 of 2 resources") {
		t.Fatal("filter not applied live")
	}
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyRune, '[', 0))
	if !waitForScreenText(t, app, screen, "Invalid filter") {
		t.Fatal("invalid expression not reported")
	}
	app.app.QueueUpdate(func() {
		if app.filter != "api" || len(app.visibleObjects) != 1 {
			t.Error("invalid filter replaced last valid rows")
		}
	})
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyEsc, 0, 0))
	if !waitForScreenText(t, app, screen, "2 of 2 resources") {
		t.Fatal("escape failed to restore filter")
	}
}

func TestObsoleteRefreshCannotReplaceNewView(t *testing.T) {
	app := qualityApp(t)
	cache := cluster.NewResourceCache(dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), 0)
	app.cache = cache
	app.objects = []*unstructured.Unstructured{podObject("current", "default")}
	app.renderCurrent()
	generation := app.generation
	app.queueUpdate(context.Background(), func() { app.applyResourceRefresh(cache, generation) })
	// Invalidate the queued request before starting the event loop.
	app.generation++
	screen := startQualityScreen(t, app, 100, 30)
	if !waitForScreenText(t, app, screen, "current") {
		t.Fatal("stale callback replaced current rows")
	}
	app.app.QueueUpdate(func() {
		if len(app.visibleObjects) != 1 || app.visibleObjects[0].GetName() != "current" {
			t.Error("stale rows committed")
		}
	})
}

func TestQueuedUpdatesDoNotBlockAfterCancellation(t *testing.T) {
	app := qualityApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	for i := 0; i < cap(app.updates); i++ {
		app.queueUpdate(ctx, func() {})
	}
	done := make(chan struct{})
	go func() { app.queueUpdate(ctx, func() {}); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("update producer blocked after cancellation")
	}
}

func TestLogStreamStopsOnExit(t *testing.T) {
	app := qualityApp(t)
	started, stopped := make(chan struct{}), make(chan struct{})
	app.options.LogStreamer = func(ctx context.Context, _ logs.PodLogTarget, opts logs.RequestOptions, w io.Writer) error {
		if opts.TailLines == nil || *opts.TailLines != 1000 {
			t.Error("missing bounded initial log tail")
		}
		close(started)
		_, _ = io.WriteString(w, "[red] literal\n")
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}
	screen := startQualityScreen(t, app, 100, 30)
	app.app.QueueUpdateDraw(func() { app.showLogsView([]logs.PodLogTarget{{Pod: "api", Container: "app"}}) })
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not start")
	}
	if !waitForScreenText(t, app, screen, "[red] literal") {
		t.Fatal("log markup was not displayed literally")
	}
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyEsc, 0, 0))
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stream leaked after exit")
	}
}
