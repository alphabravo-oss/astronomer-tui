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
