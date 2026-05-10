# AI assistant

AI is disabled by default. Enable it in `~/.config/astrotui/config.yaml`:

```yaml
ai:
  enabled: true
  provider: ollama
  model: llama3.1:8b
  endpoint: http://127.0.0.1:11434/api/generate
```

Providers are `anthropic`, `openai`, and `ollama`. The code uses a single `LLMProvider` interface with complete and streaming methods.

Default API key environment variables:

- `ANTHROPIC_API_KEY`
- `OPENAI_API_KEY`

Ollama defaults to `http://127.0.0.1:11434/api/generate` and does not require an API key. Unit tests use `MockProvider` and local HTTP servers; real provider tests are opt-in via `ASTROTUI_AI_REAL=1`.
