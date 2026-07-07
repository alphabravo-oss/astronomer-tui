package ui

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/alphabravo-oss/astrotui/internal/ai"
	"github.com/alphabravo-oss/astrotui/internal/cluster"
)

func TestAIClusterContextIncludesViewAndSelection(t *testing.T) {
	app := New(Options{Client: &cluster.Client{Context: "kind-kind", Namespace: "default"}})
	app.currentGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "api", "namespace": "default"},
	}}
	obj.SetName("api")
	app.visibleObjects = []*unstructured.Unstructured{obj}
	app.table.Select(1, 0)

	ctx := app.aiClusterContext()
	for _, want := range []string{"kind-kind", "pods", "api"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("context missing %q:\n%s", want, ctx)
		}
	}
}

func TestChatRequestBuildsPrompt(t *testing.T) {
	req := ai.ChatRequest("View: pods", "why is it failing?")
	if !strings.Contains(req.Prompt, "View: pods") || !strings.Contains(req.Prompt, "why is it failing?") {
		t.Fatalf("unexpected chat prompt %q", req.Prompt)
	}
	if req.System == "" {
		t.Fatal("expected a system prompt")
	}
}

func TestParseAICommand(t *testing.T) {
	if cmd, ok := parseCommand(":ai"); !ok || cmd.Kind != "ai" {
		t.Fatalf("unexpected ai command %#v %v", cmd, ok)
	}
}

func TestTruncateString(t *testing.T) {
	if truncateString("short", 100) != "short" {
		t.Fatal("short string should be unchanged")
	}
	got := truncateString(strings.Repeat("x", 50), 10)
	if !strings.HasSuffix(got, "(truncated)") || len(got) <= 10 {
		t.Fatalf("unexpected truncation %q", got)
	}
}
