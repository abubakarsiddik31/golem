# Golem

Golem is a Go-first framework for building dependable AI agents: typed dependencies and outputs, explicit tools, composable models, and an observable execution loop.

It takes inspiration from the ergonomics of Python agent frameworks such as Pydantic AI, but follows Go's strengths instead: compile-time contracts, `context.Context`, explicit error handling, small interfaces, and standard-library-friendly integrations.

## Status

v0.3.0 — the core execution contract includes typed agents, evidence-preserving runs, self-correction, retries with fallback models, streaming, structured output, explicit tool deadlines and choice, opt-in ordered parallel tool execution, multimodal image input, history processing with a trim builtin, token/request/tool-call usage bounds, deterministic test models, and provider adapters for OpenAI-compatible APIs (twelve services), Anthropic, Google Gemini, Azure OpenAI, and AWS Bedrock. The guides publish as a documentation site. The public API remains intentionally small; additive changes only until v1.

## Direction

- Make a useful agent the shortest path: configure a model, declare tools, run with dependencies, get a typed result.
- Make important behavior explicit: model calls, tool execution, iteration limits, usage, and validation are visible in the run result.
- Keep infrastructure replaceable: applications choose models, tracing, storage, and transport through narrow interfaces.
- Prefer Go-native composition over ports of Python metaprogramming.

Read [the foundation brief](docs/foundation.md) before proposing a new public abstraction. Contributor and coding-agent rules live in [AGENTS.md](AGENTS.md).

## Quick start

```go
client, err := openai.New(openai.Config{
    APIKey: os.Getenv("OPENAI_API_KEY"),
    Model:  "gpt-4o-mini",
})
agent, err := golem.New[struct{}, string](client,
    golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
        return r.Message.Content, nil
    }),
)
result, err := agent.Run(ctx, golem.RunContext[struct{}]{}, "Reply with exactly the word: pong")
```

Every run returns the typed output, the full normalized conversation (`result.Messages`, durable additive-only JSON), and cumulative usage — and fails with a `RunError` carrying an inspectable stage (`model`, `tool`, `decode`, `loop`, `usage`) that preserves the cause for `errors.Is` and `errors.As`.

## Documentation

Guides are the source of truth for each capability; this README only indexes them.

| Guide | Covers |
| --- | --- |
| [Getting started](docs/guides/getting-started.md) | The smallest agent, result shape, error stages |
| [Providers](docs/guides/providers.md) | OpenAI-compatible and Anthropic adapters, error classification |
| [Tools and dependencies](docs/guides/tools-and-dependencies.md) | Typed tools, dependencies, and controlled parallel execution |
| [Tool timeouts](docs/guides/tool-timeouts.md) | Context-aware deadlines for individual tool calls |
| [Conversations and history](docs/guides/conversations-and-history.md) | Multi-turn runs, durable message JSON, history trimming |
| [Multimodal input](docs/guides/multimodal-input.md) | Images in prompts, per-provider mapping |
| [Structured output](docs/guides/structured-output.md) | Output schemas, tool-mode output, `DecodeJSON` |
| [Self-correction](docs/guides/self-correction.md) | Output and tool rejection budgets (`ModelRetry`) |
| [Retries](docs/guides/retries.md) | Transient model failures, backoff, fallback models |
| [Streaming](docs/guides/streaming.md) | `RunStream`, the streaming capability port, SSE adapters |
| [Usage limits](docs/guides/usage-limits.md) | Bounding tokens, requests, and tool calls |
| [Testing without a provider](docs/guides/testing.md) | Deterministic fakes, contract assertions |

Design decisions live in [docs/adr/](docs/adr/); each guide links the ADR that decided its behavior.

## Examples

Runnable programs live in [examples/](examples/); provider-backed ones print instructions and exit unless their API key is set.

| Example | Shows |
| --- | --- |
| [`minimal`](examples/minimal/main.go) | Smallest agent against an OpenAI-compatible API |
| [`tools`](examples/tools/main.go) | Typed tool with a run dependency |
| [`structured-output`](examples/structured-output/main.go) | Output schema + JSON decoding |
| [`structured-output-tool`](examples/structured-output-tool/main.go) | Tool-mode structured output |
| [`streaming`](examples/streaming/main.go) | `RunStream` printing fragments as they arrive |
| [`conversation`](examples/conversation/main.go) | Interactive multi-turn chat with history |
| [`self-correction`](examples/self-correction/main.go) | Tool rejecting correctable arguments |
| [`fallback`](examples/fallback/main.go) | Primary model with a fallback and a request bound |
| [`anthropic`](examples/anthropic/main.go) | Anthropic Messages API adapter |
| [`gemini`](examples/gemini/main.go) | Google Gemini GenerateContent adapter |
| [`azure`](examples/azure/main.go) | Azure OpenAI deployment adapter |
| [`bedrock`](examples/bedrock/main.go) | AWS Bedrock Converse adapter with SigV4 |
| [`testing-without-a-provider`](examples/testing-without-a-provider/main.go) | Scripted fake model, offline and deterministic |

```bash
OPENAI_API_KEY=sk-... go run ./examples/minimal
go run ./examples/testing-without-a-provider   # no credentials needed
```

## Package shape

```text
golem/        Agent configuration and typed run API
model/        Provider-neutral model request/response contract
tool/         Tool declarations and execution contracts
providers/    Stdlib-only adapters implementing model.Model
internal/     Execution loop and non-public mechanics
examples/     Runnable programs per capability
docs/guides/  Feature guides (source of truth for behavior)
docs/adr/     Decisions that shape the public contracts
```

## Development

```bash
go test ./...
go vet ./...
```

The guides double as the published documentation site; preview it with `mkdocs serve` — see [docs/website.md](docs/website.md).
