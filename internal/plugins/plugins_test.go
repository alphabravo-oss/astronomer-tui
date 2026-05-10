package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadK9sPlugin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.yaml")
	data := []byte("plugins:\n  stern:\n    description: Tail logs\n    scopes: [po]\n    shortCut: Ctrl-L\n    command: stern\n    args: [\"$NAME\"]\n    background: false\n    confirm: true\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if file.Plugins["stern"].Command != "stern" || !file.Plugins["stern"].Confirm {
		t.Fatalf("unexpected plugin %#v", file.Plugins["stern"])
	}
}

func TestLoadMergedAndPlan(t *testing.T) {
	dir := t.TempDir()
	k9s := filepath.Join(dir, "k9s.yaml")
	native := filepath.Join(dir, "native.yaml")
	if err := os.WriteFile(k9s, []byte("plugins:\n  logs:\n    command: stern\n    args: [\"$NAME\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(native, []byte("plugins:\n  logs:\n    command: kubectl\n    args: [\"logs\", \"${NAME}\"]\n    confirm: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := LoadMerged(k9s, native)
	if err != nil {
		t.Fatal(err)
	}
	plan := Plan(file.Plugins["logs"], map[string]string{"NAME": "api"})
	if plan.Command != "kubectl" || len(plan.Args) != 2 || plan.Args[1] != "api" || !plan.Confirm {
		t.Fatalf("unexpected plan %#v", plan)
	}
	cmd := Command(plan)
	if cmd.Path == "" {
		t.Fatal("expected executable command")
	}
}
