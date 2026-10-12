package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScopedCustomPrecedenceAndPatterns(t *testing.T) {
	root := t.TempDir()
	overlay := ContextDir(root, "cluster", "team/production")
	if err := os.MkdirAll(overlay, 0700); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{filepath.Join(root, "aliases.yaml"): "aliases:\n  pay: pods payments\n", filepath.Join(root, "views.yaml"): "views:\n  v1/pods:\n    columns: [NAME]\n  v1/pods@kube.*:\n    columns: [STATUS]\n  v1/pods@kube-system:\n    columns: [AGE]\n", filepath.Join(overlay, "views.yaml"): "views:\n  pay:\n    columns: [NAME, READY]\n"} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c, err := LoadScopedCustom(root, "cluster", "team/production")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ alias, ns, want string }{{"", "kube-system", "AGE"}, {"", "kube-public", "STATUS"}, {"", "other", "NAME"}, {"pay", "kube-system", "NAME"}} {
		v, ok := c.ViewFor("v1/pods", tc.alias, tc.ns)
		if !ok || v.Columns[0] != tc.want {
			t.Fatalf("%+v: %+v", tc, v)
		}
	}
	other, err := LoadScopedCustom(root, "cluster", "other")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := other.Views["pay"]; ok {
		t.Fatal("context overlay leaked")
	}
	if err := os.WriteFile(filepath.Join(overlay, "aliases.yaml"), []byte("aliases:\n  a: b\n  b: a\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadScopedCustom(root, "cluster", "team/production"); err == nil {
		t.Fatal("overlay cycle accepted")
	}
}

func TestContextPreferencesIsolationAndReadOnly(t *testing.T) {
	root := t.TempDir()
	dir := ContextDir(root, "cluster", "prod")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	base := Default()
	base.ReadOnly = true
	for _, body := range []string{"policy: confirm-destructive\ntheme: dim\nnamespace: payments\nview: deployments\n", "k9s:\n  readOnly: true\n  skin: dark\n  namespace:\n    active: payments\n  view:\n    active: deployments\n"} {
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := ApplyContext(root, "cluster", "prod", base)
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.ReadOnly || cfg.Contexts["prod"].Namespace != "payments" || cfg.Contexts["prod"].View != "deployments" {
			t.Fatalf("preferences: %+v", cfg)
		}
		if base.Contexts["prod"].Namespace != "" {
			t.Fatal("overlay leaked into global config")
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("policy: accidental-open"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyContext(root, "cluster", "prod", base); err == nil {
		t.Fatal("invalid policy accepted")
	}
}
