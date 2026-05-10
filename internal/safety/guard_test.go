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
