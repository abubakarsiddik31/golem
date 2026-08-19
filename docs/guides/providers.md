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
policy decides. All shipped adapters except Bedrock implement
`model.StreamingModel` over SSE. Every adapter also translates image
parts on user messages to its native multimodal form; the per-provider
differences live in [Multimodal input](multimodal-input.md).

## Example

- `examples/minimal` — OpenAI-compatible.
- `examples/anthropic` — Anthropic Messages API.
- `examples/gemini` — Google Gemini GenerateContent API.
- `examples/azure` — Azure OpenAI deployments.
- `examples/bedrock` — AWS Bedrock Converse with SigV4.

```bash
OPENAI_API_KEY=sk-... go run ./examples/minimal
ANTHROPIC_API_KEY=sk-... go run ./examples/anthropic
GEMINI_API_KEY=... go run ./examples/gemini
AZURE_OPENAI_API_KEY=... AZURE_OPENAI_ENDPOINT=https://my-resource.openai.azure.com \
    AZURE_OPENAI_DEPLOYMENT=gpt-4o AZURE_OPENAI_API_VERSION=2024-10-21 \
    go run ./examples/azure
AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=... AWS_REGION=us-east-1 \
    go run ./examples/bedrock
```

```go
openaiClient, _ := openai.New(openai.Config{APIKey: key, Model: "gpt-4o-mini"})
anthropicClient, _ := anthropic.New(anthropic.Config{
    APIKey: key, Model: "claude-sonnet-4-5", MaxTokens: 1024,
})
geminiClient, _ := gemini.New(gemini.Config{APIKey: key, Model: "gemini-2.5-flash"})
azureClient, _ := azure.New(azure.Config{
    APIKey: key, Endpoint: "https://my-resource.openai.azure.com",
    Deployment: "gpt-4o", APIVersion: "2024-10-21",
})
bedrockClient, _ := bedrock.New(bedrock.Config{
    Credentials: bedrock.Credentials{AccessKeyID: id, SecretAccessKey: secret},
    Region: "us-east-1", Model: "us.anthropic.claude-sonnet-4-5-20250929-v1:0",
})
```

## API surface

- `openai.New(openai.Config{APIKey, BaseURL, Model, HTTPClient})`
- `anthropic.New(anthropic.Config{APIKey, BaseURL, Model, MaxTokens, HTTPClient})`
- `gemini.New(gemini.Config{APIKey, BaseURL, Model, HTTPClient})`
- `azure.New(azure.Config{APIKey, Endpoint, Deployment, APIVersion, HTTPClient})`
- `bedrock.New(bedrock.Config{Credentials, Region, Model, MaxTokens, BaseURL, HTTPClient})`
- Errors per adapter: `APIError|TransportError|DecodeError`

## Gotchas

- Many providers speak the OpenAI chat-completions wire format; the
  OpenAI adapter serves them all through `BaseURL`:

  | Provider | BaseURL |
  | --- | --- |
  | Groq | `https://api.groq.com/openai/v1` |
  | OpenRouter | `https://openrouter.ai/api/v1` |
  | DeepSeek | `https://api.deepseek.com/v1` |
  | Mistral | `https://api.mistral.ai/v1` |
  | xAI (Grok) | `https://api.x.ai/v1` |
  | Perplexity | `https://api.perplexity.ai` |
  | Cerebras | `https://api.cerebras.ai/v1` |
  | Fireworks | `https://api.fireworks.ai/inference/v1` |
  | Together | `https://api.together.xyz/v1` |
  | Cohere (compatibility) | `https://api.cohere.com/compatibility/v1` |
  | Ollama (local) | `http://localhost:11434/v1` |
  | vLLM (local) | `http://localhost:8000/v1` |

  Compatibility is the providers' own promise — verify structured output
  and streaming support against their docs; some gate `json_schema`
  responses or `stream_options` behind specific model versions.
- Anthropic requires a positive `max_tokens`; zero selects
  `anthropic.DefaultMaxTokens` (1024).
- `anthropic` merges consecutive user-side messages (tool results plus a
  following prompt) into one turn — the Messages API expects alternating
  roles.
- Gemini function calls carry no provider ID: the adapter generates
  stable ones and correlates function responses by tool name.
- Azure OpenAI shares the OpenAI wire format but addresses models by
  deployment URL with an explicit `api-version`; there is no default
  version, because versions gate feature support — structured output
  needs one that supports `response_format`.
- Gemini structured output maps to `generationConfig` JSON responses
  (`responseMimeType` + `responseSchema`); Gemini accepts a JSON-Schema
  subset — no `additionalProperties` — and rejects JSON response mode
  combined with function calling, so a request carrying both an output
  schema and tool declarations fails at request encoding with a
  `DecodeError` pointing at tool-mode output (`golem.WithOutputTool`).
- The Gemini SSE stream has no terminal sentinel: it ends at EOF, so a
  truncated stream cannot be detected the way the OpenAI-compatible
  (`[DONE]`) and Anthropic (`message_stop`) adapters do.
- Structured output maps to OpenAI `response_format`, Anthropic
  `output_config`, and Gemini `generationConfig`; OpenAI and Anthropic
  require strict-conformant schemas (see
  [Structured output](structured-output.md)).
- Bedrock credentials are wired in explicitly — the adapter never reads
  the AWS environment or credential chain; requests are SigV4-signed
  with the standard library. Bedrock streaming is not implemented yet
  (ConverseStream uses AWS binary event-stream framing, not SSE), and
  `OutputSchema` maps to the Converse `outputConfig` json_schema format
  with the schema passed as a string.
- Deciding conventions live in `docs/adr/0003-provider-adapter-conventions.md`.
