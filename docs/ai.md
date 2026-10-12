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

Every UI request opens a scrollable preview showing the exact outgoing request and configured provider/endpoint. Tab moves to Cancel/Send; no request is sent until Send. Secret manifests are blocked. Common credential fields, literal environment values, annotations, bearer tokens and private keys are redacted. Arbitrary logs may contain sensitive text the redactor cannot recognize: review the preview.

Requests are capped at 64 KiB after redaction, streams at 1 MiB of output, and calls at 30 seconds. Escape cancels an active response. Provider error events are surfaced. AI-generated action suggestions are displayed for manual review; they never execute an action automatically. Log summaries have a separate view so ongoing log ingestion cannot overwrite them.
