package ui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/config"
	"github.com/alphabravo-oss/astrotui/internal/plugins"
)

func TestConfiguredResourceAliasesResolveChainsAndRejectUnsafeTargets(t *testing.T) {
	app := New(Options{CommandAliases: map[string]string{
		"pp":       "v1/pods",
		"chain":    "pp",
		"cycle-a":  "cycle-b",
		"cycle-b":  "cycle-a",
		"filtered": "pod fred app=api",
	}})
	for _, alias := range []string{"pp", "chain"} {
		got, ok := app.resolveResource(alias)
		if !ok || got != (schema.GroupVersionResource{Version: "v1", Resource: "pods"}) {
			t.Fatalf("alias %q resolved to %#v, %v", alias, got, ok)
		}
	}
	for _, alias := range []string{"cycle-a", "filtered"} {
		if _, ok := app.resolveResource(alias); ok {
			t.Fatalf("unsafe alias %q unexpectedly resolved", alias)
		}
	}
}

func TestConfiguredAliasesAppearOnceInPalette(t *testing.T) {
	app := New(Options{
		Client:         &cluster.Client{},
		CommandAliases: map[string]string{"pp": "pods"},
	})
	count := 0
	for _, entry := range app.paletteEntries() {
		if entry.ID == "resource:pp" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one configured alias palette entry, got %d", count)
	}
}

func TestShortcutForEventMatchesK9sForms(t *testing.T) {
	tests := []struct {
		event *tcell.EventKey
		want  string
	}{
		{tcell.NewEventKey(tcell.KeyRune, ')', tcell.ModNone), "Shift-0"},
		{tcell.NewEventKey(tcell.KeyCtrlL, 0, tcell.ModCtrl), "Ctrl-L"},
		{tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModAlt), "Alt-X"},
		{tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModShift), "Shift-P"},
		{tcell.NewEventKey(tcell.KeyRune, 'p', tcell.ModNone), "p"},
	}
	for _, test := range tests {
		if got := shortcutForEvent(test.event); got != test.want {
			t.Fatalf("%s => %q, want %q", test.event.Name(), got, test.want)
		}
	}
}

func TestHotKeyOverrideAndHelpEscaping(t *testing.T) {
	app := New(Options{HotKeys: map[string]config.HotKey{
		"Shift-0": {ShortCut: "Shift-0", Command: "namespace all", Description: "pods [red]", Override: true},
		"[":       {ShortCut: "[", Command: "pods"},
	}})
	event := tcell.NewEventKey(tcell.KeyRune, ')', tcell.ModNone)
	if !app.executeHotKey(event, true) || app.options.Client.Namespace != "" {
		t.Fatal("expected override hotkey to switch to all namespaces")
	}
	if app.executeHotKey(event, false) {
		t.Fatal("override hotkey also matched non-override pass")
	}
	if text := app.helpText(); !strings.Contains(text, "pods "+tview.Escape("[red]")) {
		t.Fatalf("expected tview markup escaping in help text: %q", text)
	}
	if text := app.helpText(); !strings.Contains(text, tview.Escape("[")+"  hotkey") {
		t.Fatalf("expected shortcut markup escaping in help text: %q", text)
	}
}

func TestPluginShortcutHonorsScopeOverrideAndAmbiguity(t *testing.T) {
	var executions int
	app := New(Options{
		Client: &cluster.Client{Context: "dev", Namespace: "default"},
		PluginExecutor: func(plugins.CommandPlan) error {
			executions++
			return nil
		},
	})
	app.currentGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	app.pluginFile = plugins.File{Plugins: map[string]plugins.Plugin{
		"pod":      {Scopes: []string{"po"}, ShortCut: "p", Command: "kubectl"},
		"service":  {Scopes: []string{"svc"}, ShortCut: "p", Command: "kubectl"},
		"override": {Scopes: []string{"all"}, ShortCut: "Shift-1", Override: true, Command: "kubectl"},
	}}
	if !app.executePluginShortcut(tcell.NewEventKey(tcell.KeyRune, 'p', tcell.ModNone), false) || executions != 1 {
		t.Fatalf("expected one in-scope plugin execution, got %d", executions)
	}
	if !app.executePluginShortcut(tcell.NewEventKey(tcell.KeyRune, '!', tcell.ModNone), true) || executions != 2 {
		t.Fatalf("expected override plugin execution, got %d", executions)
	}
	app.pluginFile.Plugins["duplicate"] = plugins.Plugin{Scopes: []string{"all"}, ShortCut: "p", Command: "kubectl"}
	if !app.executePluginShortcut(tcell.NewEventKey(tcell.KeyRune, 'p', tcell.ModNone), false) || executions != 2 {
		t.Fatalf("ambiguous shortcut must be consumed without execution, got %d", executions)
	}
}
