package theme

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaultSkin(t *testing.T) {
	skin, err := Load(filepath.Join(t.TempDir(), "missing.yaml"), "dim")
	if err != nil {
		t.Fatal(err)
	}
	if skin.Name != "dim" {
		t.Fatalf("expected dim, got %s", skin.Name)
	}
}

func TestWatcherEmitsReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skin.yaml")
	if err := os.WriteFile(path, []byte("name: one\naccent: green\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := Watcher{Path: path, Fallback: "dark", Interval: 10 * time.Millisecond}.Watch(ctx)
	first := waitUpdate(t, updates)
	if first.Err != nil || first.Skin.Name != "one" {
		t.Fatalf("unexpected first update %#v", first)
	}
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(path, []byte("name: two\naccent: blue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := waitUpdate(t, updates)
	if second.Err != nil || second.Skin.Name != "two" {
		t.Fatalf("unexpected second update %#v", second)
	}
}

func waitUpdate(t *testing.T, updates <-chan Update) Update {
	t.Helper()
	select {
	case update := <-updates:
		return update
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for theme update")
	}
	return Update{}
}

func TestLoadCustomSkin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skin.yaml")
	if err := os.WriteFile(path, []byte("name: custom\nfg: white\nbg: black\naccent: green\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	skin, err := Load(path, "dark")
	if err != nil {
		t.Fatal(err)
	}
	if skin.Name != "custom" || skin.Accent != "green" {
		t.Fatalf("unexpected skin %#v", skin)
	}
}

func TestLoadK9sSkinTranslation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skin.yaml")
	data := "k9s:\n  body: {fgColor: dodgerblue, bgColor: black}\n  frame:\n    border: {fgColor: blue, focusColor: aqua}\n    status: {errorColor: orangered, pendingColor: darkorange}\n  views:\n    table:\n      fgColor: white\n      bgColor: black\n      cursorBgColor: fuchsia\n      header: {fgColor: orange}\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	skin, err := Load(path, "dark")
	if err != nil {
		t.Fatal(err)
	}
	if skin.Name != "k9s" || skin.FG != "white" || skin.Accent != "orange" || skin.Selection != "fuchsia" || skin.Error != "orangered" {
		t.Fatalf("unexpected translated skin %#v", skin)
	}
}

func TestLoadRejectsOversizedAndUnknownNativeSkinFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skin.yaml")
	if err := os.WriteFile(path, make([]byte, (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, "dark"); err == nil {
		t.Fatal("expected oversized skin rejection")
	}
	if err := os.WriteFile(path, []byte("name: custom\nunknown: red\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, "dark"); err == nil {
		t.Fatal("expected unknown native field rejection")
	}
}
