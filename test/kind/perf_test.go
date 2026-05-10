package kind

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/palette"
	"github.com/alphabravo-oss/astrotui/internal/ui"
	"github.com/alphabravo-oss/astrotui/internal/ui/views"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
)

const (
	maxBinaryBytes     = 30 * 1024 * 1024
	maxWorkingSetBytes = 150 * 1024 * 1024
)

func TestPerfHarness(t *testing.T) {
	if raceDetectorEnabled {
		t.Skip("strict perf thresholds are enforced by make perf without the race detector")
	}
	t.Run("stripped binary size", testBinarySize)
	t.Run("palette latency", testPaletteLatency)
	t.Run("render latency", testRenderLatency)
	t.Run("first frame latency", testFirstFrameLatency)
	t.Run("idle cpu", testIdleCPU)
	t.Run("working set memory", testWorkingSetMemory)
	t.Run("cache memory", testCacheMemory)
	t.Run("headless config failure startup", testHeadlessConfigFailureStartup)
}

func testBinarySize(t *testing.T) {
	out := buildPerfBinary(t)
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > maxBinaryBytes {
		t.Fatalf("binary is %d bytes, exceeds %d", info.Size(), maxBinaryBytes)
	}
}

func testPaletteLatency(t *testing.T) {
	entries := make([]palette.Entry, 10_000)
	for i := range entries {
		entries[i] = palette.Entry{
			ID:       fmt.Sprintf("pod/default/api-%d", i),
			Title:    fmt.Sprintf("pod/default/api-%d", i),
			Category: palette.Resources,
		}
	}
	idx := palette.NewIndex(entries)
	start := time.Now()
	matches := idx.Search("api-9999", 20)
	elapsed := time.Since(start)
	if len(matches) == 0 {
		t.Fatal("expected palette matches")
	}
	if elapsed > 50*time.Millisecond {
		t.Fatalf("palette search took %s, exceeds 50ms", elapsed)
	}
}

func testRenderLatency(t *testing.T) {
	renderer := views.GenericRenderer{
		Resource: schema.GroupVersionResource{Version: "v1", Resource: "pods"},
		Columns:  []string{"NAME", "NAMESPACE", "STATUS", "AGE"},
	}
	objects := make([]*unstructured.Unstructured, 5_000)
	for i := range objects {
		obj := &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{"name": fmt.Sprintf("api-%04d", i), "namespace": "default"},
			"status":   map[string]any{"phase": "Running"},
		}}
		obj.SetName(fmt.Sprintf("api-%04d", i))
		obj.SetNamespace("default")
		objects[i] = obj
	}
	start := time.Now()
	rows, visible := ui.BuildRows(renderer, objects, "api-4999", 0, false)
	elapsed := time.Since(start)
	if len(rows) != 1 || len(visible) != 1 {
		t.Fatalf("expected one filtered row, got rows=%d visible=%d", len(rows), len(visible))
	}
	if elapsed > 16*time.Millisecond {
		t.Fatalf("render/filter pass took %s, exceeds 16ms", elapsed)
	}
}

func testFirstFrameLatency(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	objects := make([]k8sruntime.Object, 1_000)
	for i := range objects {
		objects[i] = perfPod(i)
	}
	client := fake.NewSimpleDynamicClient(k8sruntime.NewScheme(), objects...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cache := cluster.NewResourceCache(client, 0)
	cache.Start(ctx)
	if !cache.WaitForSync(ctx, gvr) {
		t.Fatal("pod informer did not sync")
	}
	renderer := views.Builtins()[gvr]
	start := time.Now()
	listed, err := cache.List(gvr, "default", nil)
	if err != nil {
		t.Fatal(err)
	}
	rows, visible := ui.BuildRows(renderer, listed, "", 0, false)
	elapsed := time.Since(start)
	if len(rows) != len(objects) || len(visible) != len(objects) {
		t.Fatalf("expected %d first-frame rows, got rows=%d visible=%d", len(objects), len(rows), len(visible))
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("first frame after cache sync took %s, exceeds 100ms", elapsed)
	}
}

func testIdleCPU(t *testing.T) {
	renderer := views.GenericRenderer{
		Resource: schema.GroupVersionResource{Version: "v1", Resource: "pods"},
		Columns:  []string{"NAME", "NAMESPACE", "STATUS", "AGE"},
	}
	objects := make([]*unstructured.Unstructured, 1_000)
	for i := range objects {
		obj := &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{"name": fmt.Sprintf("api-%04d", i), "namespace": "default"},
			"status":   map[string]any{"phase": "Running"},
		}}
		obj.SetName(fmt.Sprintf("api-%04d", i))
		obj.SetNamespace("default")
		objects[i] = obj
	}
	rows, visible := ui.BuildRows(renderer, objects, "", 0, false)
	if len(rows) != len(objects) || len(visible) != len(objects) {
		t.Fatalf("expected populated idle view, got rows=%d visible=%d", len(rows), len(visible))
	}
	runtime.GC()
	before := processCPUTime(t)
	const sample = 2 * time.Second
	time.Sleep(sample)
	used := processCPUTime(t) - before
	if used > sample/50 {
		t.Fatalf("idle CPU used %s over %s, exceeds 2%% budget", used, sample)
	}
}

func testWorkingSetMemory(t *testing.T) {
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	entries := make([]palette.Entry, 10_000)
	for i := range entries {
		entries[i] = palette.Entry{
			ID:       fmt.Sprintf("pod/default/api-%d", i),
			Title:    fmt.Sprintf("pod/default/api-%d", i),
			Category: palette.Resources,
		}
	}
	idx := palette.NewIndex(entries)
	_ = idx.Search("api-9999", 20)
	renderer := views.GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, Columns: []string{"NAME", "NAMESPACE", "STATUS", "AGE"}}
	objects := make([]*unstructured.Unstructured, 5_000)
	for i := range objects {
		obj := &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{"name": fmt.Sprintf("api-%04d", i), "namespace": "default"},
			"status":   map[string]any{"phase": "Running"},
		}}
		obj.SetName(fmt.Sprintf("api-%04d", i))
		obj.SetNamespace("default")
		objects[i] = obj
	}
	_, _ = ui.BuildRows(renderer, objects, "api", 0, false)
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	var used uint64
	if after.Alloc > before.Alloc {
		used = after.Alloc - before.Alloc
	}
	if used > maxWorkingSetBytes {
		t.Fatalf("working set allocated %d bytes, exceeds %d", used, maxWorkingSetBytes)
	}
}

func testCacheMemory(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	objects := make([]k8sruntime.Object, 5_000)
	for i := range objects {
		objects[i] = perfPod(i)
	}
	client := fake.NewSimpleDynamicClient(k8sruntime.NewScheme(), objects...)
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cache := cluster.NewResourceCache(client, 0)
	cache.Start(ctx)
	if !cache.WaitForSync(ctx, gvr) {
		t.Fatal("pod informer did not sync")
	}
	listed, err := cache.List(gvr, "default", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != len(objects) {
		t.Fatalf("expected %d cached pods, got %d", len(objects), len(listed))
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	var used uint64
	if after.Alloc > before.Alloc {
		used = after.Alloc - before.Alloc
	}
	const maxCacheBytes = 30 * 1024 * 1024
	if used > maxCacheBytes {
		t.Fatalf("cache allocated %d bytes for 5,000 pods, exceeds %d", used, maxCacheBytes)
	}
}

func processCPUTime(t *testing.T) time.Duration {
	t.Helper()
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		t.Fatal(err)
	}
	user := time.Duration(usage.Utime.Sec)*time.Second + time.Duration(usage.Utime.Usec)*time.Microsecond
	system := time.Duration(usage.Stime.Sec)*time.Second + time.Duration(usage.Stime.Usec)*time.Microsecond
	return user + system
}

func perfPod(i int) *unstructured.Unstructured {
	name := fmt.Sprintf("api-%04d", i)
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":      name,
			"namespace": "default",
			"labels":    map[string]any{"app": "api"},
		},
		"status": map[string]any{"phase": "Running"},
	}}
	obj.SetName(name)
	obj.SetNamespace("default")
	return obj
}

func testHeadlessConfigFailureStartup(t *testing.T) {
	binary := buildPerfBinary(t)
	best := time.Duration(1<<63 - 1)
	for range 3 {
		cmd := exec.Command(binary, "--headless") // #nosec G204 -- perf harness runs a freshly built local binary.
		home := t.TempDir()
		cmd.Env = append(os.Environ(), "HOME="+home, "KUBECONFIG="+filepath.Join(home, "missing"))
		start := time.Now()
		_ = cmd.Run()
		if elapsed := time.Since(start); elapsed < best {
			best = elapsed
		}
	}
	if best > 300*time.Millisecond {
		t.Fatalf("best headless config-failure startup took %s, exceeds 300ms", best)
	}
}

func buildPerfBinary(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	out := filepath.Join(t.TempDir(), "astrotui")
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", out, "./cmd/astrotui") // #nosec G204 -- perf harness runs a fixed go build command.
	cmd.Dir = root
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, data)
	}
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}
