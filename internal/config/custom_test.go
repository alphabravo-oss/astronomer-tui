package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCustomConfigAndImport(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	fixtures := map[string]string{"aliases.yaml": "aliases:\n  pay: pod payments app=api\n", "hotkeys.yaml": "hotKeys:\n  payments:\n    shortCut: Ctrl-Y\n    command: pay\n", "views.yaml": "views:\n  v1/pods:\n    sortColumn: NAME:desc\n    columns: [NAME, 'APP:.metadata.labels.app', 'AGE|W']\n", "skin.yaml": "k9s:\n  body:\n    fgColor: white\n    bgColor: black\n"}
	for name, data := range fixtures {
		if err := os.WriteFile(filepath.Join(src, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ImportK9s(src, dst); err != nil {
		t.Fatal(err)
	}
	custom, err := LoadCustom(dst)
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := custom.Expand("pay")
	if err != nil || expanded != "pod payments app=api" {
		t.Fatal(expanded, err)
	}
	skin, _ := os.ReadFile(filepath.Join(dst, "skin.yaml"))
	if !strings.Contains(string(skin), "fg: white") {
		t.Fatal(string(skin))
	}
	custom.Aliases = map[string]string{"a": "b", "b": "a"}
	if _, err = custom.Expand("a"); err == nil {
		t.Fatal("cycle accepted")
	}
	if err = os.WriteFile(filepath.Join(dst, "hotkeys.yaml"), []byte("hotKeys:\n  broken:\n    unsupported: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadCustom(dst); err == nil {
		t.Fatal("unsupported fields swallowed")
	}
}
