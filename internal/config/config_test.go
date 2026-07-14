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

func TestLoadRejectsRemotePlaintextAIEndpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := "ai:\n  enabled: true\n  provider: openai\n  endpoint: http://example.com/v1\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected non-loopback plaintext endpoint rejection")
	}
	data = "ai:\n  enabled: true\n  provider: ollama\n  endpoint: http://127.0.0.1:11434/api/generate\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("expected loopback endpoint to be allowed: %v", err)
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

func TestImportK9sRejectsDestinationSymlink(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "skin.yaml"), []byte("skin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.Symlink(target, filepath.Join(dst, "skin.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportK9s(src, dst); err == nil {
		t.Fatal("expected symlink destination rejection")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("outside target should not be created: %v", err)
	}
}

func TestImportK9sRejectsOversizedSource(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	data := make([]byte, MaxCompatibilityFileBytes+1)
	if err := os.WriteFile(filepath.Join(src, "aliases.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportK9s(src, dst); err == nil {
		t.Fatal("expected oversized import source rejection")
	}
	if _, err := os.Stat(filepath.Join(dst, "aliases.yaml")); !os.IsNotExist(err) {
		t.Fatalf("oversized import must not create destination: %v", err)
	}
}
