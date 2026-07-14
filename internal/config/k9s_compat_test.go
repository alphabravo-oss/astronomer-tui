package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAliasesValidatesAndBoundsK9sFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aliases.yaml")
	if err := os.WriteFile(path, []byte("aliases:\n  pp: v1/pods\n  crb: rbac.authorization.k8s.io/v1/clusterrolebindings\n  chained: pp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	aliases, err := LoadAliases(path)
	if err != nil {
		t.Fatal(err)
	}
	if aliases["pp"] != "v1/pods" || aliases["chained"] != "pp" {
		t.Fatalf("unexpected aliases %#v", aliases)
	}
	if err := os.WriteFile(path, []byte("aliases:\n  'bad alias': v1/pods\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAliases(path); err == nil {
		t.Fatal("expected invalid alias rejection")
	}
	if err := os.WriteFile(path, []byte("aliases:\n  '[red]': v1/pods\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAliases(path); err == nil {
		t.Fatal("expected markup alias rejection")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(MaxCompatibilityFileBytes+1))), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAliases(path); err == nil {
		t.Fatal("expected oversized compatibility file rejection")
	}
}

func TestLoadHotKeysCanonicalizesAndRejectsDuplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hotkeys.yaml")
	data := "hotKeys:\n  pods:\n    shortCut: Shift-0\n    description: View pods\n    command: pp\n    override: true\n  deployments:\n    shortCut: Ctrl-L\n    command: dp\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	hotkeys, err := LoadHotKeys(path)
	if err != nil {
		t.Fatal(err)
	}
	if !hotkeys["Shift-0"].Override || hotkeys["Ctrl-L"].Command != "dp" {
		t.Fatalf("unexpected hotkeys %#v", hotkeys)
	}
	duplicate := "hotKeys:\n  one: {shortCut: Shift-0, command: pods}\n  two: {shortCut: shift-0, command: deployments}\n"
	if err := os.WriteFile(path, []byte(duplicate), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHotKeys(path); err == nil {
		t.Fatal("expected duplicate shortcut rejection")
	}
	control := "hotKeys:\n  bad: {shortCut: g, command: pods, description: \"bad\\ttext\"}\n"
	if err := os.WriteFile(path, []byte(control), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHotKeys(path); err == nil {
		t.Fatal("expected control character description rejection")
	}
}

func TestCanonicalShortcut(t *testing.T) {
	for input, want := range map[string]string{"g": "g", "G": "g", "ctrl-l": "Ctrl-L", "ALT-x": "Alt-X", "Shift-0": "Shift-0"} {
		got, err := CanonicalShortcut(input)
		if err != nil || got != want {
			t.Fatalf("%q => %q err=%v, want %q", input, got, err, want)
		}
	}
	for _, invalid := range []string{"", "Ctrl-Shift-X", "Enter", "é"} {
		if _, err := CanonicalShortcut(invalid); err == nil {
			t.Fatalf("expected invalid shortcut %q", invalid)
		}
	}
}

func TestLoadAppliesImportedK9sReadOnlyUnlessNativeOverrides(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(filepath.Join(dir, "config.k9s.yaml"), []byte("k9s:\n  readOnly: true\n  refreshRate: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Safety.DefaultPolicy != "read-only" {
		t.Fatalf("expected imported read-only policy, got %#v", cfg.Safety)
	}
	if err := os.WriteFile(configPath, []byte("safety:\n  defaultPolicy: full\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Safety.DefaultPolicy != "full" {
		t.Fatalf("native policy must override import, got %#v", cfg.Safety)
	}
}
