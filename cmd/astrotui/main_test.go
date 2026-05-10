package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunInvalidConfigExitsConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "astrotui")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("unknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"--headless"}); code != exConfig {
		t.Fatalf("expected exConfig, got %d", code)
	}
}

func TestRunVersionSkipsConfigAndKube(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if code := run([]string{"--version"}); code != 0 {
		t.Fatalf("expected version success, got %d", code)
	}
}

func TestRunMissingKubeconfigExitsUsage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KUBECONFIG", filepath.Join(home, "missing"))
	if code := run([]string{"--headless"}); code != exUsage {
		t.Fatalf("expected exUsage, got %d", code)
	}
}
