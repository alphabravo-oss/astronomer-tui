package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadHotkeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hotkeys.yaml")
	content := `hotKeys:
  viewPods:
    shortCut: shift-p
    command: pods
    description: View pods
  bad:
    shortCut: x
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	hks := LoadHotkeys(path)
	if len(hks) != 1 || hks[0].Key != "shift-p" || hks[0].Command != "pods" {
		t.Fatalf("unexpected hotkeys %#v", hks)
	}
	if v := LoadHotkeys(filepath.Join(dir, "missing.yaml")); len(v) != 0 {
		t.Fatalf("expected empty for missing, got %#v", v)
	}
}
