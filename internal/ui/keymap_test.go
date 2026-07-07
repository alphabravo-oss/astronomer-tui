package ui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestKeymapDispatch(t *testing.T) {
	k := &Keymap{}
	var got string
	k.Add(runeBinding('d', "describe", "Describe", "table", true, func() { got = "describe" }))
	k.Add(keyBinding(tcell.KeyCtrlD, "delete", "Delete", "table", true, func() { got = "delete" }))

	if !k.handle(tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModNone)) || got != "describe" {
		t.Fatalf("expected describe binding to fire, got %q", got)
	}
	if !k.handle(tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModCtrl)) || got != "delete" {
		t.Fatalf("expected delete binding to fire, got %q", got)
	}
	if k.handle(tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModNone)) {
		t.Fatal("unregistered key should not be handled")
	}
}

func TestKeymapHelpRowsOnlyVisible(t *testing.T) {
	k := &Keymap{}
	k.Add(runeBinding('d', "describe", "Describe", "table", true, func() {}))
	k.Add(runeBinding('Z', "secret", "Hidden", "table", false, func() {}))
	rows := k.HelpRows()
	if len(rows) != 1 || rows[0][0] != "d" || !strings.Contains(rows[0][1], "describe") {
		t.Fatalf("unexpected help rows %#v", rows)
	}
}

func TestBuildTableKeymapCoversCoreVerbs(t *testing.T) {
	app := New(Options{})
	app.runCtx = t.Context()
	km := app.buildTableKeymap()
	want := map[string]bool{"describe": false, "yaml": false, "delete": false, "logs": false, "exec": false, "port-forward": false, "palette": false, "help": false}
	for _, b := range km.bindings {
		if _, ok := want[b.Name]; ok {
			want[b.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("table keymap missing %q binding", name)
		}
	}
}

func TestHelpOverlayReflectsRegistry(t *testing.T) {
	app := New(Options{})
	app.runCtx = t.Context()
	app.tableKeymap = app.buildTableKeymap()
	text := app.helpOverlayText()
	for _, want := range []string{"describe", "delete", "Commands", ":ctx", ":q"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help overlay missing %q:\n%s", want, text)
		}
	}
}

func TestParseHotkeySpec(t *testing.T) {
	if k, r, ok := parseHotkeySpec("a"); !ok || k != tcell.KeyRune || r != 'a' {
		t.Fatalf("single rune: %v %q %v", k, r, ok)
	}
	if k, _, ok := parseHotkeySpec("f5"); !ok || k != tcell.KeyF5 {
		t.Fatalf("f5: %v %v", k, ok)
	}
	if k, _, ok := parseHotkeySpec("ctrl-t"); !ok || k != tcell.KeyCtrlT {
		t.Fatalf("ctrl-t: %v %v", k, ok)
	}
	if _, r, ok := parseHotkeySpec("shift-1"); !ok || r != '!' {
		t.Fatalf("shift-1: %q %v", r, ok)
	}
	if _, r, ok := parseHotkeySpec("shift-p"); !ok || r != 'P' {
		t.Fatalf("shift-p: %q %v", r, ok)
	}
	if _, _, ok := parseHotkeySpec("meta-super-x"); ok {
		t.Fatal("unknown spec should fail")
	}
}
