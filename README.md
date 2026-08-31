<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/brand/logo/golem-wordmark-dark.svg">
    <img src="assets/brand/logo/golem-wordmark.svg" width="380" alt="golem">
  </picture>
</p>

# Golem

Golem is a Go-first framework for building dependable AI agents: typed dependencies and outputs, explicit tools, composable models, and an observable execution loop.

It takes inspiration from the ergonomics of Python agent frameworks such as Pydantic AI, but follows Go's strengths instead: compile-time contracts, `context.Context`, explicit error handling, small interfaces, and standard-library-friendly integrations.

## Status

v0.7.1 — the core execution contract includes typed agents, evidence-preserving runs — successful and failed alike, via `RunError.Partial` — self-correction, retries with fallback models, streaming on every adapter including Bedrock, structured output, explicit tool deadlines and choice, opt-in ordered parallel tool execution, multimodal image input, thinking content carried end to end with provider signatures, deferred tools that pause a run for human approval or external results, bounded history, token/request/tool-call usage bounds, request tuning, run events, and agent delegation. The common tools (web fetch, file read, command execution) ship alongside an MCP client that bridges server tools over stdio or streamable HTTP, with provider adapters for OpenAI-compatible APIs (twelve services plus the local Ollama and LM Studio runtimes), Anthropic, Google Gemini, Azure OpenAI, and AWS Bedrock. The guides publish as a documentation site. The public API remains intentionally small; additive changes only until v1.

## Direction

- Make a useful agent the shortest path: configure a model, declare tools, run with dependencies, get a typed result.
- Make important behavior explicit: model calls, tool execution, iteration limits, usage, and validation are visible in the run result.
- Keep infrastructure replaceable: applications choose models, tracing, storage, and transport through narrow interfaces.
- Prefer Go-native composition over ports of Python metaprogramming.

Read [the foundation brief](docs/foundation.md) before proposing a new public abstraction. Contributor and coding-agent rules live in [AGENTS.md](AGENTS.md), and the development roadmap lives in [docs/ROADMAP.md](docs/ROADMAP.md).

## Installation

```bash
go get github.com/abubakarsiddik31/golem
```

Golem needs Go 1.26.5 or newer and depends only on the Go standard library.

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
| [Web fetch](docs/guides/web-fetch.md) | The `webfetch` common tool: URLs as agent-readable text |
| [File read](docs/guides/file-read.md) | The `fileread` common tool: workspace files as agent-readable text |
| [Command execution](docs/guides/command-execution.md) | The `shell` common tool: one command, combined output |
| [Agent skills](docs/guides/skills.md) | The `skills` common tool: standard SKILL.md folders loaded on demand |
| [MCP client](docs/guides/mcp-client.md) | Bridging Model Context Protocol servers into agent tools |
| [Agent delegation](docs/guides/agent-delegation.md) | One agent exposed as another agent's tool |
| [Tool timeouts](docs/guides/tool-timeouts.md) | Context-aware deadlines for individual tool calls |
| [Conversations and history](docs/guides/conversations-and-history.md) | Multi-turn runs, durable message JSON, history trimming |
| [Multimodal input](docs/guides/multimodal-input.md) | Images in prompts, per-provider mapping |
| [Structured output](docs/guides/structured-output.md) | Output schemas, tool-mode output, `DecodeJSON` |
| [Self-correction](docs/guides/self-correction.md) | Output and tool rejection budgets (`ModelRetry`) |
| [Retries](docs/guides/retries.md) | Transient model failures, backoff, fallback models |
| [Streaming](docs/guides/streaming.md) | `RunStream`, the streaming capability port, SSE adapters |
| [Thinking](docs/guides/thinking.md) | Reasoning models: requesting thinking, keeping signatures, replay |
| [Run events](docs/guides/run-events.md) | Observing attempts, tool calls, and corrections as they happen |
| [Usage limits](docs/guides/usage-limits.md) | Bounding tokens, requests, and tool calls |
| [Testing without a provider](docs/guides/testing.md) | Deterministic fakes, contract assertions |
| [Deferred tools](docs/guides/deferred-tools.md) | Approvals and external results: pausing a run and resuming it |

Design decisions live in [docs/adr/](docs/adr/); each guide links the ADR that decided its behavior.

## Examples

Runnable programs live in [examples/](examples/); provider-backed ones print instructions and exit unless their API key is set.

| Example | Shows |
| --- | --- |
| [`minimal`](examples/minimal/main.go) | Smallest agent against an OpenAI-compatible API |
| [`tools`](examples/tools/main.go) | Typed tool with a run dependency |
| [`web-fetch`](examples/web-fetch/main.go) | The `webfetch` common tool fetching a local test page |
| [`file-read`](examples/file-read/main.go) | The `fileread` common tool reading a workspace file |
| [`command-execution`](examples/command-execution/main.go) | The `shell` common tool running one local command |
| [`skills`](examples/skills/main.go) | The `skills` common tool loading a standard SKILL.md folder |
| [`mcp-client`](examples/mcp-client/main.go) | MCP server bridged into agent tools over stdio |
| [`mcp-http`](examples/mcp-http/main.go) | MCP server bridged over streamable HTTP |
| [`delegation`](examples/delegation/main.go) | A specialist agent delegated to as a tool |
| [`structured-output`](examples/structured-output/main.go) | Output schema + JSON decoding |
| [`structured-output-tool`](examples/structured-output-tool/main.go) | Tool-mode structured output |
| [`streaming`](examples/streaming/main.go) | `RunStream` printing fragments as they arrive |
| [`run-events`](examples/run-events/main.go) | `WithRunEvents` printing the event sequence of a run |
| [`partial-evidence`](examples/partial-evidence/main.go) | A failed run's `RunError.Partial` evidence resumed with history, offline |
| [`thinking`](examples/thinking/main.go) | Adaptive thinking with reasoning blocks and signatures |
| [`conversation`](examples/conversation/main.go) | Interactive multi-turn chat with history |
| [`self-correction`](examples/self-correction/main.go) | Tool rejecting correctable arguments |
| [`fallback`](examples/fallback/main.go) | Primary model with a fallback and a request bound |
| [`anthropic`](examples/anthropic/main.go) | Anthropic Messages API adapter |
| [`gemini`](examples/gemini/main.go) | Google Gemini GenerateContent adapter |
| [`azure`](examples/azure/main.go) | Azure OpenAI deployment adapter |
| [`bedrock`](examples/bedrock/main.go) | AWS Bedrock Converse adapter with SigV4 |
| [`local-models`](examples/local-models/main.go) | Ollama or LM Studio through the OpenAI-compatible adapter |
| [`testing-without-a-provider`](examples/testing-without-a-provider/main.go) | Scripted fake model, offline and deterministic |

```bash
OPENAI_API_KEY=sk-... go run ./examples/minimal
GOLEM_LOCAL_BASE_URL=http://localhost:11434/v1 go run ./examples/local-models   # Ollama or LM Studio
go run ./examples/testing-without-a-provider   # no credentials needed
go run ./examples/partial-evidence             # no credentials needed
```

## Package shape

```text
golem/        Agent configuration and typed run API
model/        Provider-neutral model request/response contract
tool/         Tool declarations and execution contracts
webfetch/     Common tool: fetch a URL as agent-readable text
fileread/     Common tool: read a file as agent-readable text
shell/        Common tool: run one command, return combined output
mcp/          Model Context Protocol client bridging servers into tools
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

The guides double as the published documentation site; preview it with `mkdocs serve` — see [docs/website.md](docs/website.md). Logo usage guidelines and brand assets live in [assets/brand/](assets/brand/README.md).

## Community

- [Contributing guide](CONTRIBUTING.md) — setup, required checks, and how changes are reviewed
- [Security policy](SECURITY.md) — reporting vulnerabilities privately
- [Code of conduct](CODE_OF_CONDUCT.md) — standards for participation

## License

Released under the [MIT License](LICENSE).
