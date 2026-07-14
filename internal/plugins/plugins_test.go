package plugins

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadK9sPlugin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.yaml")
	data := []byte("plugins:\n  stern:\n    description: Tail logs\n    scopes: [po]\n    shortCut: Ctrl-L\n    override: true\n    command: stern\n    args: [\"$NAME\"]\n    background: false\n    confirm: true\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if file.Plugins["stern"].Command != "stern" || !file.Plugins["stern"].Confirm || !file.Plugins["stern"].Override {
		t.Fatalf("unexpected plugin %#v", file.Plugins["stern"])
	}
}

func TestLoadRejectsOversizedPluginFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.yaml")
	if err := os.WriteFile(path, make([]byte, MaxFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected oversized plugin file rejection")
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

func TestPlanNeverExpandsExecutable(t *testing.T) {
	plan := Plan(Plugin{Command: "$NAME", Args: []string{"$NAME"}}, map[string]string{"NAME": "api"})
	if plan.Command != "$NAME" || len(plan.Args) != 1 || plan.Args[0] != "api" {
		t.Fatalf("unexpected plan %#v", plan)
	}
}

func TestMutatingPluginPlanExpandsPreviewArguments(t *testing.T) {
	plan := Plan(Plugin{
		Command: "kubectl", Args: []string{"delete", "pod", "$NAME"}, Mutates: true,
		PreviewCommand: "kubectl", PreviewArgs: []string{"delete", "pod", "$NAME", "--dry-run=server", "-o", "yaml"},
	}, map[string]string{"NAME": "api"})
	if !plan.Mutates || plan.PreviewCommand != "kubectl" || len(plan.PreviewArgs) != 6 || plan.PreviewArgs[2] != "api" {
		t.Fatalf("unexpected mutating plan %#v", plan)
	}
	if _, err := Preview(context.Background(), CommandPlan{Mutates: true}, 1024); err == nil {
		t.Fatal("expected missing preview command to block mutating plugin")
	}
}
