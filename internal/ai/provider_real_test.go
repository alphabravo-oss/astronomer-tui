package ai

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/config"
)

func TestRealProvidersOptIn(t *testing.T) {
	if os.Getenv("ASTROTUI_AI_REAL") != "1" {
		t.Skip("set ASTROTUI_AI_REAL=1 to run real provider tests")
	}
	t.Run("openai", func(t *testing.T) {
		requireEnv(t, "OPENAI_API_KEY")
		testRealProvider(t, config.AIConfig{Enabled: true, Provider: "openai"})
	})
	t.Run("anthropic", func(t *testing.T) {
		requireEnv(t, "ANTHROPIC_API_KEY")
		testRealProvider(t, config.AIConfig{Enabled: true, Provider: "anthropic"})
	})
	t.Run("ollama", func(t *testing.T) {
		host := os.Getenv("OLLAMA_HOST")
		if host == "" {
			t.Skip("OLLAMA_HOST is not set")
		}
		testRealProvider(t, config.AIConfig{Enabled: true, Provider: "ollama", Endpoint: strings.TrimRight(host, "/") + "/api/generate"})
	})
}

func testRealProvider(t *testing.T, cfg config.AIConfig) {
	t.Helper()
	provider, err := NewProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := provider.Complete(ctx, Request{
		System: "You are a concise test responder.",
		Prompt: "Reply with exactly: ok",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp.Text), "ok") {
		t.Fatalf("unexpected response %q", resp.Text)
	}
}

func requireEnv(t *testing.T, name string) {
	t.Helper()
	if os.Getenv(name) == "" {
		t.Skip(name + " is not set")
	}
}
