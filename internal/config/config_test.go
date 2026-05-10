package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsWhenMissing(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Safety.ProdPattern == "" {
		t.Fatal("expected default prod pattern")
	}
	if cfg.AI.Enabled {
		t.Fatal("AI must be disabled by default")
	}
}

func TestLoadRejectsInvalidPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("contexts:\n  prod:\n    policy: nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestLoadRejectsUnknownFieldWithLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("theme: dark\nunknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected ValidationError, got %T", err)
	}
	if validation.Line != 2 || validation.Col != 1 {
		t.Fatalf("expected line 2 col 1, got line %d col %d: %v", validation.Line, validation.Col, validation)
	}
}

func TestImportK9s(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	files := map[string]string{
		"config.yaml":  "k9s-config\n",
		"skin.yaml":    "k9s-skin\n",
		"hotkeys.yaml": "k9s-hotkeys\n",
		"aliases.yaml": "k9s-aliases\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	results, err := ImportK9s(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{"config.yaml", "skin.yaml", "hotkeys.yaml", "aliases.yaml"}
	if len(results) != len(wantOrder) {
		t.Fatalf("unexpected results %#v", results)
	}
	for i, want := range wantOrder {
		if results[i] != (ImportResult{File: want, Status: "imported"}) {
			t.Fatalf("result %d got %#v", i, results[i])
		}
	}
	second, err := ImportK9s(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range second {
		if result.Status != "skipped" {
			t.Fatalf("expected idempotent skip, got %#v", second)
		}
	}
	if err := os.WriteFile(filepath.Join(dst, "skin.yaml"), []byte("local-change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := ImportK9s(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if third[1] != (ImportResult{File: "skin.yaml", Status: "conflict"}) {
		t.Fatalf("expected skin conflict, got %#v", third)
	}
}
