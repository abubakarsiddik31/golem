# Providers

## Purpose

Connect an agent to a real generation API through a stdlib-only adapter,
with typed, retry-classified errors.

## When to use

Whenever a run should reach a live model. Anything implementing
`model.Model` works; the shipped adapters cover OpenAI-compatible APIs
and Anthropic.

## How it works

Adapters translate the provider-neutral `model.Request` to their wire
format and normalize responses back. They never retry on their own:
failures are classified through `model.RetryableError` (408, 429, 5xx,
transport faults — never context cancellation) and the runner's retry
policy decides. Both adapters implement `model.StreamingModel` over SSE.

## Example

- `examples/minimal` — OpenAI-compatible.
- `examples/anthropic` — Anthropic Messages API.

```bash
OPENAI_API_KEY=sk-... go run ./examples/minimal
ANTHROPIC_API_KEY=sk-... go run ./examples/anthropic
```

```go
openaiClient, _ := openai.New(openai.Config{APIKey: key, Model: "gpt-4o-mini"})
anthropicClient, _ := anthropic.New(anthropic.Config{
    APIKey: key, Model: "claude-sonnet-4-5", MaxTokens: 1024,
})
```

## API surface

- `openai.New(openai.Config{APIKey, BaseURL, Model, HTTPClient})`
- `anthropic.New(anthropic.Config{APIKey, BaseURL, Model, MaxTokens, HTTPClient})`
- Errors: `openai.APIError|TransportError|DecodeError`,
  `anthropic.APIError|TransportError|DecodeError`

## Gotchas

- A configurable `BaseURL` serves Groq, OpenRouter, DeepSeek, Together,
  Ollama, and vLLM with the OpenAI adapter.
- Anthropic requires a positive `max_tokens`; zero selects
  `anthropic.DefaultMaxTokens` (1024).
- `anthropic` merges consecutive user-side messages (tool results plus a
  following prompt) into one turn — the Messages API expects alternating
  roles.
- Structured output maps to OpenAI `response_format` and Anthropic
  `output_config`; both providers require strict-conformant schemas (see
  [Structured output](structured-output.md)).
- Deciding conventions live in `docs/adr/0003-provider-adapter-conventions.md`.
