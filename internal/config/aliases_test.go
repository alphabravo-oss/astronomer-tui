package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAliasesMergesLaterWins(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.yaml")
	b := filepath.Join(dir, "b.yaml")
	if err := os.WriteFile(a, []byte("aliases:\n  pp: v1/pods\n  dd: apps/v1/deployments\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("aliases:\n  pp: v1/pods-override\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Later path (b) wins for pp.
	got := LoadAliases(b, a)
	if got["pp"] != "v1/pods" || got["dd"] != "apps/v1/deployments" {
		t.Fatalf("unexpected merge (a wins as later) %#v", got)
	}
	got = LoadAliases(a, b)
	if got["pp"] != "v1/pods-override" {
		t.Fatalf("expected b to override pp, got %#v", got)
	}
	// Missing files are skipped.
	if m := LoadAliases(filepath.Join(dir, "missing.yaml")); len(m) != 0 {
		t.Fatalf("expected empty for missing, got %#v", m)
	}
}
