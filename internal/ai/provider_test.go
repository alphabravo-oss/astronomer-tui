package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/alphabravo-oss/astrotui/internal/config"
)

func TestMockProvider(t *testing.T) {
	resp, err := ExplainFailure(context.Background(), MockProvider{Response: "fix image tag"}, "spec", "status", "events")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "fix image tag" {
		t.Fatalf("unexpected response %q", resp.Text)
	}
}

func TestNewProviderDefaults(t *testing.T) {
	provider, err := NewProvider(config.AIConfig{Enabled: true, Provider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	httpProvider, ok := provider.(HTTPProvider)
	if !ok {
		t.Fatalf("expected HTTPProvider, got %T", provider)
	}
	if httpProvider.Model != "gpt-4o-mini" || httpProvider.APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("unexpected provider %#v", httpProvider)
	}
}

func TestOpenAIProvider(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	var gotAuth string
	var gotModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotModel = body.Model
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"openai ok"}}]}`))
	}))
	defer server.Close()
	provider := HTTPProvider{Provider: "openai", Model: "test-model", Endpoint: server.URL, APIKeyEnv: "OPENAI_API_KEY", Client: server.Client()} // #nosec G101 -- test fixture uses a fake local-server key.
	resp, err := provider.Complete(context.Background(), Request{System: "sys", Prompt: "prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "openai ok" || gotAuth != "Bearer test-key" || gotModel != "test-model" {
		t.Fatalf("resp=%#v auth=%q model=%q", resp, gotAuth, gotModel)
	}
}

func TestOpenAIProviderStream(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	var gotStream bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotStream = body.Stream
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"open\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ai\"},\"finish_reason\":\"stop\"}]}\n\n"))
	}))
	defer server.Close()
	provider := HTTPProvider{Provider: "openai", Endpoint: server.URL, APIKeyEnv: "OPENAI_API_KEY", Client: server.Client()} // #nosec G101 -- test fixture uses a fake local-server key.
	chunks, err := provider.Stream(context.Background(), Request{System: "sys", Prompt: "prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if got := collectChunks(t, chunks); got != "openai" || !gotStream {
		t.Fatalf("got stream %q stream=%v", got, gotStream)
	}
}

func TestAnthropicProvider(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	var gotKey string
	var gotVersion string
	var gotSystem []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		var body struct {
			System []map[string]any `json:"system"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotSystem = body.System
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"anthropic ok"}]}`))
	}))
	defer server.Close()
	provider := HTTPProvider{Provider: "anthropic", Endpoint: server.URL, APIKeyEnv: "ANTHROPIC_API_KEY", Client: server.Client()} // #nosec G101 -- test fixture uses a fake local-server key.
	resp, err := provider.Complete(context.Background(), Request{System: "sys", Prompt: "prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "anthropic ok" || gotKey != "test-key" || gotVersion == "" || !hasAnthropicCacheControl(gotSystem) {
		t.Fatalf("resp=%#v key=%q version=%q system=%#v", resp, gotKey, gotVersion, gotSystem)
	}
}

func TestAnthropicProviderStream(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	var gotStream bool
	var gotSystem []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool             `json:"stream"`
			System []map[string]any `json:"system"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotStream = body.Stream
		gotSystem = body.System
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"anthropic\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer server.Close()
	provider := HTTPProvider{Provider: "anthropic", Endpoint: server.URL, APIKeyEnv: "ANTHROPIC_API_KEY", Client: server.Client()} // #nosec G101 -- test fixture uses a fake local-server key.
	chunks, err := provider.Stream(context.Background(), Request{System: "sys", Prompt: "prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if got := collectChunks(t, chunks); got != "anthropic" || !gotStream || !hasAnthropicCacheControl(gotSystem) {
		t.Fatalf("got stream %q stream=%v system=%#v", got, gotStream, gotSystem)
	}
}

func TestOllamaProviderStream(t *testing.T) {
	var gotStream bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotStream = body.Stream
		_, _ = w.Write([]byte("{\"response\":\"oll\",\"done\":false}\n"))
		_, _ = w.Write([]byte("{\"response\":\"ama\",\"done\":true}\n"))
	}))
	defer server.Close()
	provider := HTTPProvider{Provider: "ollama", Endpoint: server.URL, Client: server.Client()}
	chunks, err := provider.Stream(context.Background(), Request{System: "sys", Prompt: "prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if got := collectChunks(t, chunks); got != "ollama" || !gotStream {
		t.Fatalf("got stream %q stream=%v", got, gotStream)
	}
}

func TestProviderRequiresAPIKey(t *testing.T) {
	env := "ASTROTUI_TEST_MISSING_KEY"
	_ = os.Unsetenv(env)
	provider := HTTPProvider{Provider: "openai", APIKeyEnv: env}
	if _, err := provider.Complete(context.Background(), Request{}); err == nil {
		t.Fatal("expected missing key error")
	}
}

func collectChunks(t *testing.T, chunks <-chan Chunk) string {
	t.Helper()
	var got strings.Builder
	for chunk := range chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		got.WriteString(chunk.Text)
	}
	return got.String()
}

func hasAnthropicCacheControl(system []map[string]any) bool {
	if len(system) == 0 {
		return false
	}
	cache, ok := system[0]["cache_control"].(map[string]any)
	return ok && cache["type"] == "ephemeral"
}

func TestStreamingFeatureHelpers(t *testing.T) {
	provider := &streamProvider{chunks: []Chunk{{Text: "one"}, {Text: " two"}, {Done: true}}}
	chunks, err := StreamManifest(context.Background(), provider, "kind: Pod")
	if err != nil {
		t.Fatal(err)
	}
	if got := collectChunks(t, chunks); got != "one two" || provider.last.System == "" || !strings.Contains(provider.last.Prompt, "kind: Pod") {
		t.Fatalf("got stream %q request %#v", got, provider.last)
	}
}

func TestLogQuestionRequestIncludesContextAndQuestion(t *testing.T) {
	req := LogQuestionRequest("api failed", "why")
	if !strings.Contains(req.System, "follow-up") || !strings.Contains(req.Prompt, "api failed") || !strings.Contains(req.Prompt, "why") {
		t.Fatalf("unexpected request %#v", req)
	}
}

func TestStreamingFeatureHelperError(t *testing.T) {
	provider := &streamProvider{err: errors.New("boom")}
	if _, err := StreamLogSummary(context.Background(), provider, "logs"); err == nil {
		t.Fatal("expected stream error")
	}
}

type streamProvider struct {
	chunks []Chunk
	err    error
	last   Request
}

func (s *streamProvider) Complete(context.Context, Request) (Response, error) {
	return Response{}, nil
}

func (s *streamProvider) Stream(_ context.Context, req Request) (<-chan Chunk, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.last = req
	ch := make(chan Chunk, len(s.chunks))
	for _, chunk := range s.chunks {
		ch <- chunk
	}
	close(ch)
	return ch, nil
}
