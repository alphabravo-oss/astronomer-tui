package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadViews(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "views.yaml")
	content := `views:
  v1/pods:
    columns: [NAME, STATUS, "CUSTOM:.metadata.annotations.team"]
  apps/v1/deployments:
    columns: []
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	views := LoadViews(path)
	pods, ok := views["v1/pods"]
	if !ok || len(pods.Columns) != 3 || pods.Columns[2] != "CUSTOM:.metadata.annotations.team" {
		t.Fatalf("unexpected pods view %#v", pods)
	}
	// Empty column lists are dropped.
	if _, ok := views["apps/v1/deployments"]; ok {
		t.Fatal("empty column spec should be dropped")
	}
	if v := LoadViews(filepath.Join(dir, "nope.yaml")); len(v) != 0 {
		t.Fatalf("expected empty for missing, got %#v", v)
	}
}
