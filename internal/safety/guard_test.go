package safety

import (
	"testing"

	"github.com/alphabravo-oss/astrotui/internal/config"
)

func TestProdContextRequiresTypedConfirmation(t *testing.T) {
	cfg := config.Default()
	if !RequiresConfirmation(cfg, "customer-prod", Delete) {
		t.Fatal("expected prod context confirmation")
	}
	if err := Authorize(cfg, "customer-prod", Delete, "wrong"); err == nil {
		t.Fatal("expected confirmation error")
	}
	if err := Authorize(cfg, "customer-prod", Delete, "customer-prod"); err != nil {
		t.Fatal(err)
	}
}

func TestReadOnlyPolicyBlocksDestructive(t *testing.T) {
	cfg := config.Default()
	cfg.Contexts = map[string]config.ContextConfig{"dev": {Policy: "read-only"}}
	if err := Authorize(cfg, "dev", Patch, ""); err != ErrReadOnly {
		t.Fatalf("expected ErrReadOnly, got %v", err)
	}
}

func TestReadOnlyBlocksPluginsAndPortForward(t *testing.T) {
	cfg := config.Default()
	cfg.Contexts = map[string]config.ContextConfig{"prod": {Policy: "read-only"}}
	for _, verb := range []Verb{Plugin, PortForward} {
		if err := Authorize(cfg, "prod", verb, "prod"); err != ErrReadOnly {
			t.Fatalf("expected %s to be blocked, got %v", verb, err)
		}
	}
}

func TestDefaultPolicyAppliesWithoutContextOverride(t *testing.T) {
	cfg := config.Default()
	cfg.Safety.DefaultPolicy = "read-only"
	if err := Authorize(cfg, "dev", Delete, ""); err != ErrReadOnly {
		t.Fatalf("expected default read-only policy, got %v", err)
	}
	cfg.Contexts = map[string]config.ContextConfig{"dev": {Policy: "full"}}
	if err := Authorize(cfg, "dev", Delete, ""); err != nil {
		t.Fatalf("context override should win: %v", err)
	}
}
