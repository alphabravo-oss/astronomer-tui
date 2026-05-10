//revive:disable:exported package-comments
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/config"
)

type Request struct {
	System  string
	Prompt  string
	Context map[string]string
}

type Response struct {
	Text string
}

type Chunk struct {
	Text string
	Err  error
	Done bool
}

type LLMProvider interface {
	Complete(context.Context, Request) (Response, error)
	Stream(context.Context, Request) (<-chan Chunk, error)
}

type MockProvider struct {
	Response string
}

func (m MockProvider) Complete(_ context.Context, _ Request) (Response, error) {
	if m.Response == "" {
		return Response{Text: "mock response"}, nil
	}
	return Response{Text: m.Response}, nil
}

func (m MockProvider) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	ch := make(chan Chunk, 1)
	go func() {
		defer close(ch)
		resp, err := m.Complete(ctx, req)
		if err != nil {
			ch <- Chunk{Err: err, Done: true}
			return
		}
		ch <- Chunk{Text: resp.Text}
		ch <- Chunk{Done: true}
	}()
	return ch, nil
}

type HTTPProvider struct {
	Provider  string
	Model     string
	Endpoint  string
	APIKeyEnv string
	Client    *http.Client
}

func NewProvider(cfg config.AIConfig) (LLMProvider, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.Provider == "" {
		return nil, fmt.Errorf("ai.provider is required when ai.enabled is true")
	}
	model := cfg.Model
	if model == "" {
		model = defaultModel(cfg.Provider)
	}
	apiKeyEnv := cfg.APIKeyEnv
	if apiKeyEnv == "" {
		apiKeyEnv = defaultAPIKeyEnv(cfg.Provider)
	}
	return HTTPProvider{
		Provider:  cfg.Provider,
		Model:     model,
		Endpoint:  cfg.Endpoint,
		APIKeyEnv: apiKeyEnv,
	}, nil
}

func (h HTTPProvider) Complete(ctx context.Context, req Request) (Response, error) {
	switch h.Provider {
	case "ollama":
		return h.ollama(ctx, req)
	case "openai":
		return h.openAI(ctx, req)
	case "anthropic":
		return h.anthropic(ctx, req)
	default:
		return Response{}, fmt.Errorf("unknown provider %q", h.Provider)
	}
}

func (h HTTPProvider) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	switch h.Provider {
	case "ollama":
		return h.ollamaStream(ctx, req)
	case "openai":
		return h.openAIStream(ctx, req)
	case "anthropic":
		return h.anthropicStream(ctx, req)
	default:
		return nil, fmt.Errorf("unknown provider %q", h.Provider)
	}
}

func (h HTTPProvider) ollamaStream(ctx context.Context, req Request) (<-chan Chunk, error) {
	endpoint := h.Endpoint
	if endpoint == "" {
		endpoint = "http://127.0.0.1:11434/api/generate"
	}
	body := map[string]any{"model": h.Model, "prompt": req.System + "\n\n" + req.Prompt, "stream": true}
	return h.streamJSONLines(ctx, endpoint, "", body, func(data []byte) Chunk {
		var decoded struct {
			Response string `json:"response"`
			Done     bool   `json:"done"`
		}
		if err := json.Unmarshal(data, &decoded); err != nil {
			return Chunk{Err: err, Done: true}
		}
		return Chunk{Text: decoded.Response, Done: decoded.Done}
	})
}

func (h HTTPProvider) openAIStream(ctx context.Context, req Request) (<-chan Chunk, error) {
	endpoint := h.Endpoint
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/chat/completions"
	}
	key, err := h.apiKey()
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"model":  h.model("gpt-4o-mini"),
		"stream": true,
		"messages": []map[string]string{
			{"role": "system", "content": req.System},
			{"role": "user", "content": promptWithContext(req)},
		},
	}
	return h.streamServerSentEvents(ctx, endpoint, "Bearer "+key, body, openAIStreamChunk)
}

func (h HTTPProvider) anthropicStream(ctx context.Context, req Request) (<-chan Chunk, error) {
	endpoint := h.Endpoint
	if endpoint == "" {
		endpoint = "https://api.anthropic.com/v1/messages"
	}
	key, err := h.apiKey()
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"model":      h.model("claude-3-5-sonnet-latest"),
		"max_tokens": 2048,
		"stream":     true,
		"system":     anthropicSystem(req.System),
		"messages": []map[string]string{{
			"role":    "user",
			"content": promptWithContext(req),
		}},
	}
	data, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", key)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	return h.streamHTTPResponse(httpReq, serverSentEventChunk(anthropicStreamChunk)), nil
}

func (h HTTPProvider) streamJSONLines(ctx context.Context, endpoint, authHeader string, body any, decode func([]byte) Chunk) (<-chan Chunk, error) {
	data, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		httpReq.Header.Set("Authorization", authHeader)
	}
	return h.streamHTTPResponse(httpReq, decode), nil
}

func (h HTTPProvider) streamServerSentEvents(ctx context.Context, endpoint, authHeader string, body any, decode func([]byte) Chunk) (<-chan Chunk, error) {
	data, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		httpReq.Header.Set("Authorization", authHeader)
	}
	return h.streamHTTPResponse(httpReq, func(data []byte) Chunk {
		return serverSentEventChunk(decode)(data)
	}), nil
}

func serverSentEventChunk(decode func([]byte) Chunk) func([]byte) Chunk {
	return func(data []byte) Chunk {
		line := strings.TrimSpace(string(data))
		if !strings.HasPrefix(line, "data:") {
			return Chunk{}
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			return Chunk{}
		}
		if payload == "[DONE]" {
			return Chunk{Done: true}
		}
		return decode([]byte(payload))
	}
}

func (h HTTPProvider) streamHTTPResponse(req *http.Request, decode func([]byte) Chunk) <-chan Chunk {
	ch := make(chan Chunk, 8)
	go func() {
		defer close(ch)
		resp, err := h.httpClient().Do(req) // #nosec G704 -- LLM endpoints are explicit user configuration or provider defaults.
		if err != nil {
			ch <- Chunk{Err: err, Done: true}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if err := checkStatus(resp); err != nil {
			ch <- Chunk{Err: err, Done: true}
			return
		}
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			chunk := decode(line)
			if chunk.Text != "" || chunk.Err != nil || chunk.Done {
				ch <- chunk
			}
			if chunk.Err != nil || chunk.Done {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- Chunk{Err: err, Done: true}
			return
		}
		ch <- Chunk{Done: true}
	}()
	return ch
}

func openAIStreamChunk(data []byte) Chunk {
	var decoded struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return Chunk{Err: err, Done: true}
	}
	if len(decoded.Choices) == 0 {
		return Chunk{}
	}
	return Chunk{Text: decoded.Choices[0].Delta.Content, Done: decoded.Choices[0].FinishReason != ""}
}

func anthropicStreamChunk(data []byte) Chunk {
	var decoded struct {
		Type  string `json:"type"`
		Delta struct {
			Text string `json:"text"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return Chunk{Err: err, Done: true}
	}
	return Chunk{Text: decoded.Delta.Text, Done: decoded.Type == "message_stop"}
}

func (h HTTPProvider) ollama(ctx context.Context, req Request) (Response, error) {
	endpoint := h.Endpoint
	if endpoint == "" {
		endpoint = "http://127.0.0.1:11434/api/generate"
	}
	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	body := map[string]any{"model": h.Model, "prompt": req.System + "\n\n" + req.Prompt, "stream": false}
	data, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(httpReq)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkStatus(resp); err != nil {
		return Response{}, err
	}
	var decoded struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Response{}, err
	}
	return Response{Text: decoded.Response}, nil
}

func (h HTTPProvider) openAI(ctx context.Context, req Request) (Response, error) {
	endpoint := h.Endpoint
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/chat/completions"
	}
	key, err := h.apiKey()
	if err != nil {
		return Response{}, err
	}
	body := map[string]any{
		"model": h.model("gpt-4o-mini"),
		"messages": []map[string]string{
			{"role": "system", "content": req.System},
			{"role": "user", "content": promptWithContext(req)},
		},
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := h.postJSON(ctx, endpoint, key, "Bearer "+key, body, &decoded); err != nil {
		return Response{}, err
	}
	if len(decoded.Choices) == 0 {
		return Response{}, fmt.Errorf("openai response contained no choices")
	}
	return Response{Text: decoded.Choices[0].Message.Content}, nil
}

func (h HTTPProvider) anthropic(ctx context.Context, req Request) (Response, error) {
	endpoint := h.Endpoint
	if endpoint == "" {
		endpoint = "https://api.anthropic.com/v1/messages"
	}
	key, err := h.apiKey()
	if err != nil {
		return Response{}, err
	}
	body := map[string]any{
		"model":      h.model("claude-3-5-sonnet-latest"),
		"max_tokens": 2048,
		"system":     anthropicSystem(req.System),
		"messages": []map[string]string{{
			"role":    "user",
			"content": promptWithContext(req),
		}},
	}
	data, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", key)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	resp, err := h.httpClient().Do(httpReq)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkStatus(resp); err != nil {
		return Response{}, err
	}
	var decoded struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Response{}, err
	}
	var parts []string
	for _, part := range decoded.Content {
		if part.Type == "text" && part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	if len(parts) == 0 {
		return Response{}, fmt.Errorf("anthropic response contained no text")
	}
	return Response{Text: strings.Join(parts, "\n")}, nil
}

func anthropicSystem(system string) any {
	if strings.TrimSpace(system) == "" {
		return system
	}
	return []map[string]any{{
		"type":          "text",
		"text":          system,
		"cache_control": map[string]string{"type": "ephemeral"},
	}}
}

func (h HTTPProvider) postJSON(ctx context.Context, endpoint, _ string, authHeader string, body any, out any) error {
	data, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		httpReq.Header.Set("Authorization", authHeader)
	}
	resp, err := h.httpClient().Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkStatus(resp); err != nil {
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (h HTTPProvider) apiKey() (string, error) {
	env := h.APIKeyEnv
	if env == "" {
		env = defaultAPIKeyEnv(h.Provider)
	}
	key := os.Getenv(env)
	if key == "" {
		return "", fmt.Errorf("%s is not set", env)
	}
	return key, nil
}

func (h HTTPProvider) model(fallback string) string {
	if h.Model != "" {
		return h.Model
	}
	return fallback
}

func (h HTTPProvider) httpClient() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func promptWithContext(req Request) string {
	if len(req.Context) == 0 {
		return req.Prompt
	}
	var b strings.Builder
	b.WriteString(req.Prompt)
	b.WriteString("\n\nContext:\n")
	for key, value := range req.Context {
		b.WriteString(key)
		b.WriteString(": ")
		b.WriteString(value)
		b.WriteByte('\n')
	}
	return b.String()
}

func checkStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("llm provider returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
}

func defaultModel(provider string) string {
	switch provider {
	case "anthropic":
		return "claude-3-5-sonnet-latest"
	case "openai":
		return "gpt-4o-mini"
	case "ollama":
		return "llama3.1:8b"
	default:
		return ""
	}
}

func defaultAPIKeyEnv(provider string) string {
	switch provider {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	default:
		return ""
	}
}
