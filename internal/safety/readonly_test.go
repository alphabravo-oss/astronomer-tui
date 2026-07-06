package safety

import (
	"testing"

	"github.com/alphabravo-oss/astrotui/internal/config"
)

func TestReadOnlyBlocksEverything(t *testing.T) {
	cfg := config.Default()
	cfg.ReadOnly = true
	if PolicyFor(cfg, "dev") != "read-only" {
		t.Fatal("expected read-only policy")
	}
	if err := Authorize(cfg, "dev", Delete, ""); err != ErrReadOnly {
		t.Fatalf("expected read-only error, got %v", err)
	}
	// Non-destructive verbs are unaffected.
	if err := Authorize(cfg, "dev", Patch, ""); err == nil {
		// Patch is destructive; ensure it's blocked too.
		t.Fatal("expected patch blocked under read-only")
	}
}
