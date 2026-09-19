<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/brand/logo/golem-wordmark-dark.svg">
    <img src="assets/brand/logo/golem-wordmark.svg" width="380" alt="Golem Wordmark">
  </picture>
</p>

<p align="center">
  <strong>A Go-first framework for building dependable, production-grade AI agents.</strong><br>
  Compile-time type safety, zero external dependencies, explicit execution control, and deep MCP integration.
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/abubakarsiddik31/golem"><img src="https://pkg.go.dev/badge/github.com/abubakarsiddik31/golem.svg" alt="Go Reference"></a>
  <a href="https://github.com/abubakarsiddik31/golem/actions/workflows/ci.yml"><img src="https://github.com/abubakarsiddik31/golem/actions/workflows/ci.yml/badge.svg" alt="CI Status"></a>
  <a href="https://abubakarsiddik31.github.io/golem/"><img src="https://img.shields.io/badge/docs-website-00ADD8.svg" alt="Documentation"></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/dependencies-0%20(stdlib%20only)-brightgreen.svg" alt="Zero Dependencies"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT"></a>
  <a href="https://github.com/abubakarsiddik31/golem/releases"><img src="https://img.shields.io/badge/release-v0.8.1-orange.svg" alt="Release v0.8.1"></a>
</p>

<p align="center">
  <a href="https://abubakarsiddik31.github.io/golem/"><strong>Read the Documentation »</strong></a>
  <br><br>
  <a href="#why-golem">Why Golem?</a> •
  <a href="#quick-start">Quick Start</a> •
  <a href="#supported-providers">Supported Providers</a> •
  <a href="#architecture">Architecture</a> •
  <a href="#batteries-included-tools">Common Tools & MCP</a> •
  <a href="#testing-without-a-provider">Testing</a> •
  <a href="#documentation">Guides Index</a> •
  <a href="#examples">Examples</a>
</p>

---

## Why Golem?

Python frameworks like Pydantic AI made agent prototyping accessible with typed schemas and clean ergonomics. However, deploying AI agents to mission-critical production systems demands the strengths of Go: high-throughput concurrency, predictable memory usage, fast execution, small deployment binaries, and compile-time correctness.

Golem bridges that gap by offering idiomatic, enterprise-ready agent abstractions designed specifically for Go engineers:

- 🛡️ **Compile-Time Type Safety & Generics**: Agents are declared as `Agent[Deps, Output]`. Tools access strongly-typed dependencies (databases, auth contexts, HTTP clients) via `RunContext[Deps]`. No `map[string]any` spaghetti or unexpected runtime reflection errors.
- 📦 **Zero External Dependencies**: Built exclusively on Go's standard library. Instant compilation, tiny container images, and a clean security profile free of supply-chain vulnerabilities.
- 🌐 **Provider Agnostic**: Native adapters for **OpenAI**, **Anthropic** (with prompt caching and thinking signatures), **Google Gemini**, **AWS Bedrock** (Converse API with SigV4), **Azure OpenAI**, and local offline models with **Ollama** and **LM Studio**.
- 🔌 **Native Model Context Protocol (MCP)**: Full-featured MCP client supporting both `stdio` and streaming HTTP transports, effortlessly turning external MCP servers into typed agent tools.
- 🔄 **Production Resilience & Self-Correction**: In-loop model self-correction (`ModelRetry`), automated multi-model fallbacks, exponential backoff, per-tool deadlines, and clean in-tool cancellation (`tool.Canceled`).
- 📊 **Auditable & Observable Evidence**: Every run produces normalized messages, durable additive JSON, live streamable run events, reasoning/thinking token capture, and `RunError.Partial`—preserving all intermediate tool results even when a run fails or gets cancelled.
- ⏸️ **Human-in-the-Loop & Deferred Execution**: Pause agent runs cleanly when tools require human sign-off or external async triggers, and resume deterministically with full preserved state.
- 🛠️ **Batteries-Included Tooling**: Layout-aware PDF extraction (`pdfextract`), multi-format document extraction (`docextract`: Word, Excel, PowerPoint, Markdown, CSV), SSR web reader (`webfetch`), workspace file accessor (`fileread`), shell command execution (`shell`), and Agent Skills progressive loading (`skills`).
- 💰 **Budget & Cost Guards**: Pre-send token estimation, token-budgeted history truncation, client-side untrusted history sanitization (`SanitizeHistory`), and user-defined price tables to calculate and cap dollar costs per run.

---

## Architecture

Golem orchestrates agents through a transparent, observable execution loop:

```text
                         ┌────────────────────────────────────────┐
                         │         golem.RunContext[Deps]         │
                         │        (Typed Run Dependencies)        │
                         └───────────────────┬────────────────────┘
                                             ▼
┌─────────────────┐          ┌───────────────────────────────┐          ┌─────────────────┐
│     Prompt      │ ───────► │     golem.Agent[Deps, Out]    │ ───────► │  Typed Output   │
│ (Text, Images,  │          │                               │          │  Result[Output] │
│ Docs, Audio)    │          │  ┌─────────────────────────┐  │          └─────────────────┘
└─────────────────┘          │  │ Observable Loop         │  │                   │
                             │  │ - Self-Correction       │  │                   ▼
                             │  │ - Retries & Fallbacks   │  │          ┌─────────────────┐
                             │  │ - Token & Cost Bounds   │  │          │ Durable Evidence│
                             │  │ - Run Events Stream     │  │          │ (Messages, Cost,│
                             │  └────────────┬────────────┘  │          │  Token Usage)   │
                             └───────────────┼───────────────┘          └─────────────────┘
                                             │
                      ┌──────────────────────┴──────────────────────┐
                      ▼                                             ▼
        ┌───────────────────────────┐                 ┌───────────────────────────┐
        │      Model Adapters       │                 │      Tools & Protocols    │
        │  • OpenAI & Azure OpenAI  │                 │  • Strongly Typed Tools   │
        │  • Anthropic Claude       │                 │  • MCP Client (stdio/HTTP)│
        │  • Google Gemini          │                 │  • PDF & Doc Extractors   │
        │  • AWS Bedrock (SigV4)    │                 │  • Web Fetch & Shell      │
        │  • Local (Ollama/LMStudio)│                 │  • Agent Skills (SKILL.md)│
        └───────────────────────────┘                 └───────────────────────────┘
```

---

## Installation

```bash
go get github.com/abubakarsiddik31/golem
```

Requires **Go 1.26.5** or newer. Zero external dependencies.

---

## Quick Start

### 1. Minimal Agent

Initialize a model adapter, create a typed agent, and execute a prompt:

```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/openai"
)

func main() {
	client, err := openai.New(openai.Config{
		APIKey: os.Getenv("OPENAI_API_KEY"),
		Model:  "gpt-4o-mini",
	})
	if err != nil {
		panic(err)
	}

	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}),
	)
	if err != nil {
		panic(err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "Why is Go ideal for AI agents?")
	if err != nil {
		panic(err)
	}

	fmt.Println(result.Output)
	fmt.Printf("Tokens: %d input, %d output\n", result.Usage.InputTokens, result.Usage.OutputTokens)
}
```

### 2. Typed Tools with Dependency Injection

Golem tools are strongly typed and receive dependencies via `RunContext[Deps]`. No global state, no untyped maps:

```go
type Database struct {
	Users map[int]string
}

// Declare a tool typed to Database dependencies
getUser := tool.MustNew(tool.Tool[Database]{
	Name:        "get_user",
	Description: "Look up a user name by their ID.",
	Schema: json.RawMessage(`{
		"type": "object",
		"properties": {"id": {"type": "integer"}},
		"required": ["id"]
	}`),
	Exec: func(ctx context.Context, db Database, args json.RawMessage) (tool.Result, error) {
		var input struct {
			ID int `json:"id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return tool.Result{}, err
		}
		name, ok := db.Users[input.ID]
		if !ok {
			return tool.Text("User not found"), nil
		}
		return tool.Text(name), nil
	},
})

// Create an agent parameterized with Database dependencies
agent, err := golem.New[Database, string](client,
	golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
		return r.Message.Content, nil
	}),
	golem.WithTools[Database, string](getUser),
)

// Run passing the typed dependency instance
db := Database{Users: map[int]string{42: "Alice"}}
result, err := agent.Run(ctx, golem.RunContext[Database]{Deps: db}, "Who is user 42?")
```

### 3. Structured Output & Schema Validation

Guarantee your agent returns strongly-typed Go structs with `golem.DecodeJSON[T]()` and strict schema enforcement:

```go
type WeatherReport struct {
	City        string  `json:"city"`
	Temperature float64 `json:"temperature_celsius"`
	Condition   string  `json:"condition"`
}

agent, err := golem.New[struct{}, WeatherReport](client,
	golem.DecodeJSON[WeatherReport](),
	golem.WithOutputSchema[struct{}, WeatherReport](json.RawMessage(`{
		"type": "object",
		"properties": {
			"city": {"type": "string"},
			"temperature_celsius": {"type": "number"},
			"condition": {"type": "string"}
		},
		"required": ["city", "temperature_celsius", "condition"],
		"additionalProperties": false
	}`)),
)

result, err := agent.Run(ctx, golem.RunContext[struct{}]{}, "Forecast for Lagos, Nigeria.")
fmt.Printf("%s: %.1f°C (%s)\n", result.Output.City, result.Output.Temperature, result.Output.Condition)
```

---

## Supported Providers

Golem ships with standard-library-only adapters for all major frontier and open-weight models:

| Provider | Adapter Package | Streaming | Thinking / Reasoning | Multimodal | Embeddings | Token Counting |
| --- | --- | :---: | :---: | :---: | :---: | :---: |
| **OpenAI** | `providers/openai` | :white_check_mark: | :white_check_mark: | :white_check_mark: | :white_check_mark: | — |
| **Anthropic** | `providers/anthropic` | :white_check_mark: | :white_check_mark: | :white_check_mark: | — | :white_check_mark: |
| **Google Gemini** | `providers/gemini` | :white_check_mark: | :white_check_mark: | :white_check_mark: | :white_check_mark: | :white_check_mark: |
| **AWS Bedrock** | `providers/bedrock` | :white_check_mark: | :white_check_mark: | :white_check_mark: | — | :white_check_mark: |
| **Azure OpenAI** | `providers/azure` | :white_check_mark: | :white_check_mark: | :white_check_mark: | :white_check_mark: | — |
| **Ollama / Local** | `providers/openai` | :white_check_mark: | :white_check_mark: | :white_check_mark: | :white_check_mark: | — |
| **OpenAI-Compatible** | `providers/openai` | :white_check_mark: | :white_check_mark: | :white_check_mark: | :white_check_mark: | — |

---

## Batteries-Included Tools

Golem includes pre-built common tools written entirely in pure Go:

| Package | Capability | Features |
| --- | --- | --- |
| **`mcp`** | Model Context Protocol Client | Connects to any MCP tool server over `stdio` or streaming HTTP (SSE). |
| **`pdfextract`** | Layout-Aware PDF Extraction | High-performance PDF parser preserving reading order, layout, tables, and embedded images. |
| **`docextract`** | Multi-Format Document Extraction | Extracts text and structure from Word (`.docx`), Excel (`.xlsx`), PowerPoint (`.pptx`), CSV, and Markdown. |
| **`webfetch`** | Web Extraction | Fetches URLs and returns clean, agent-readable text without browser overhead. |
| **`fileread`** | File Reader | Safe workspace file reading with path boundaries and clean formatting. |
| **`shell`** | Command Execution | Isolated command execution with timeout handling and combined stdout/stderr output. |
| **`skills`** | Agent Skills Loader | Discovers and loads standard `SKILL.md` skill folders on demand for progressive prompt enrichment. |

---

## Testing Without a Provider

Never mock HTTP endpoints or pay for tokens in unit tests. Golem includes `testmodel`, a fully deterministic, offline model implementation:

```go
package main

import (
	"context"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
)

func TestAgent(t *testing.T) {
	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "pong"}},
	)

	agent, _ := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}),
	)

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "ping")
	if err != nil || result.Output != "pong" {
		t.Fatalf("unexpected result: %v, output: %s", err, result.Output)
	}
}
```

---

## Documentation

The feature guides provide complete references for each capability and mirror the published documentation site at [abubakarsiddik31.github.io/golem](https://abubakarsiddik31.github.io/golem/).

| Guide | Covers |
| --- | --- |
| [Getting started](docs/guides/getting-started.md) | The smallest agent, result shape, error stages |
| [Providers](docs/guides/providers.md) | OpenAI-compatible and Anthropic adapters, error classification |
| [Embeddings](docs/guides/embeddings.md) | The `embedding.Embedder` port: queries, documents, usage |
| [Token counting](docs/guides/token-counting.md) | The `tokens.Counter` port: budgets, pre-send limits |
| [Cost](docs/guides/cost.md) | User-supplied pricing: `Result.Cost` and cost bounds |
| [Tools and dependencies](docs/guides/tools-and-dependencies.md) | Typed tools, dependencies, and controlled parallel execution |
| [Web fetch](docs/guides/web-fetch.md) | The `webfetch` common tool: URLs as agent-readable text |
| [File read](docs/guides/file-read.md) | The `fileread` common tool: workspace files as agent-readable text |
| [Command execution](docs/guides/command-execution.md) | The `shell` common tool: one command, combined output |
| [PDF extract](docs/guides/pdf-extract.md) | The `pdfextract` common tool: PDF documents as structured Markdown with tables and images |
| [Document extract](docs/guides/doc-extract.md) | The `docextract` common tool: Word, Excel, PowerPoint, Markdown, CSV, and multi-format documents |
| [Agent skills](docs/guides/skills.md) | The `skills` common tool: standard SKILL.md folders loaded on demand |
| [MCP client](docs/guides/mcp-client.md) | Bridging Model Context Protocol servers into agent tools |
| [Agent delegation](docs/guides/agent-delegation.md) | One agent exposed as another agent's tool |
| [Tool timeouts](docs/guides/tool-timeouts.md) | Context-aware deadlines for individual tool calls |
| [Conversations and history](docs/guides/conversations-and-history.md) | Multi-turn runs, durable message JSON, history trimming |
| [Multimodal input](docs/guides/multimodal-input.md) | Images, documents, audio, and video in prompts, per-provider mapping |
| [Structured output](docs/guides/structured-output.md) | Output schemas, tool-mode output, `DecodeJSON` |
| [Self-correction](docs/guides/self-correction.md) | Output and tool rejection budgets (`ModelRetry`) |
| [Retries](docs/guides/retries.md) | Transient model failures, backoff, fallback models |
| [Streaming](docs/guides/streaming.md) | `RunStream`, the streaming capability port, SSE adapters |
| [Run events](docs/guides/run-events.md) | Observing attempts, tool calls, and corrections as they happen |
| [Thinking](docs/guides/thinking.md) | Reasoning models: requesting thinking, keeping signatures, replay |
| [Usage limits](docs/guides/usage-limits.md) | Bounding tokens, requests, and tool calls |
| [Testing without a provider](docs/guides/testing.md) | Deterministic fakes, contract assertions |
| [Deferred tools](docs/guides/deferred-tools.md) | Approvals and external results: pausing a run and resuming it |

Design records live in [docs/adr/](docs/adr/); each guide links the ADR that decided its behavior.

---

## Examples

Runnable programs live in [`examples/`](examples/); provider-backed examples read their API key from the environment and exit cleanly with instructions when unset.

| Example | Shows |
| --- | --- |
| [`minimal`](examples/minimal/main.go) | Smallest agent against an OpenAI-compatible API |
| [`tools`](examples/tools/main.go) | Typed tool with a run dependency |
| [`structured-output`](examples/structured-output/main.go) | Output schema + JSON decoding |
| [`structured-output-tool`](examples/structured-output-tool/main.go) | Tool-mode structured output |
| [`conversation`](examples/conversation/main.go) | Interactive multi-turn chat with history |
| [`streaming`](examples/streaming/main.go) | `RunStream` printing fragments as they arrive |
| [`run-events`](examples/run-events/main.go) | `WithRunEvents` printing the event sequence of a run |
| [`mcp-client`](examples/mcp-client/main.go) | MCP server bridged into agent tools over stdio |
| [`mcp-http`](examples/mcp-http/main.go) | MCP server bridged over streamable HTTP |
| [`skills`](examples/skills/main.go) | The `skills` common tool loading a standard SKILL.md folder |
| [`web-fetch`](examples/web-fetch/main.go) | The `webfetch` common tool fetching a local test page |
| [`file-read`](examples/file-read/main.go) | The `fileread` common tool reading a workspace file |
| [`command-execution`](examples/command-execution/main.go) | The `shell` common tool running one local command |
| [`pdf-extract`](examples/pdf-extract/main.go) | The `pdfextract` common tool extracting tables and reading order, offline |
| [`doc-extract`](examples/doc-extract/main.go) | The `docextract` common tool extracting Word, Excel, and Markdown, offline |
| [`delegation`](examples/delegation/main.go) | A specialist agent delegated to as a tool |
| [`tool-results`](examples/tool-results/main.go) | Tools returning parts and definitive failures, offline |
| [`self-correction`](examples/self-correction/main.go) | Tool rejecting correctable arguments |
| [`fallback`](examples/fallback/main.go) | Primary model with a fallback and a request bound |
| [`token-counting`](examples/token-counting/main.go) | Pre-send limits and budget-bounded history over the `tokens.Counter` port |
| [`cost`](examples/cost/main.go) | User-supplied pricing: `Result.Cost` and cost-bounded runs, offline |
| [`embeddings`](examples/embeddings/main.go) | Semantic search over the `embedding.Embedder` port |
| [`multimodal-input`](examples/multimodal-input/main.go) | Prompts with images and document attachments |
| [`thinking`](examples/thinking/main.go) | Adaptive thinking with reasoning blocks and signatures |
| [`run-cancellation`](examples/run-cancellation/main.go) | A tool ending the run deliberately with `tool.Canceled`, resuming evidence, offline |
| [`run-ids`](examples/run-ids/main.go) | Run and conversation identity across chained and forked runs, offline |
| [`deferred-tools`](examples/deferred-tools/main.go) | Pausing runs for approvals or external results, and resuming, offline |
| [`partial-evidence`](examples/partial-evidence/main.go) | A failed run's `RunError.Partial` evidence resumed with history, offline |
| [`history-repair`](examples/history-repair/main.go) | Normalizing a damaged conversation with a report, offline |
| [`history-sanitization`](examples/history-sanitization/main.go) | Sanitizing a client-submitted history at the trust boundary, offline |
| [`anthropic`](examples/anthropic/main.go) | Anthropic Messages API adapter |
| [`gemini`](examples/gemini/main.go) | Google Gemini GenerateContent adapter |
| [`azure`](examples/azure/main.go) | Azure OpenAI deployment adapter |
| [`bedrock`](examples/bedrock/main.go) | AWS Bedrock Converse adapter with SigV4 |
| [`local-models`](examples/local-models/main.go) | Ollama or LM Studio through the OpenAI-compatible adapter |
| [`testing-without-a-provider`](examples/testing-without-a-provider/main.go) | Scripted fake model, offline and deterministic |

To run any example:

```bash
# Run with OpenAI
OPENAI_API_KEY=sk-... go run ./examples/minimal

# Run locally with Ollama or LM Studio
GOLEM_LOCAL_BASE_URL=http://localhost:11434/v1 go run ./examples/local-models

# Run offline examples (no credentials needed)
go run ./examples/testing-without-a-provider
go run ./examples/pdf-extract
go run ./examples/deferred-tools
go run ./examples/partial-evidence
```

---

## Package Structure

```text
golem/        Agent configuration and typed run API
model/        Provider-neutral model request/response contract
tool/         Tool declarations and execution contracts
mcp/          Model Context Protocol client (stdio & streaming HTTP)
pdfextract/   Common tool: layout-aware PDF parser (tables, images, text)
docextract/   Common tool: Word, Excel, PowerPoint, CSV, and Markdown extractor
webfetch/     Common tool: fetch a URL as agent-readable text
fileread/     Common tool: read a file as agent-readable text
shell/        Common tool: run one command, return combined output
skills/       Common tool: standard SKILL.md folder progressive loader
providers/    Stdlib-only adapters implementing model.Model
testmodel/    Deterministic in-memory model doubles for unit testing
internal/     Execution runner loop and private mechanics
examples/     Runnable programs per capability
docs/guides/  Feature guides (source of truth for behavior)
docs/adr/     Decisions that shape public contracts
```

---

## Status & Roadmap

Golem is currently at **v0.8.1**.

The core execution contract is frozen and verified with continuous race-detector CI, memory fuzzing, and deterministic offline tests. The public API adheres strictly to additive-only changes on the road to **v1.0.0**.

- **Resilient Execution:** Self-correction loops (`ModelRetry`), fallback models, exponential backoff, per-tool timeouts, in-tool cancellation (`tool.Canceled`), and partial evidence preservation (`RunError.Partial`).
- **Comprehensive Multimodal:** Text, images, PDF documents, audio, and video inputs mapped natively across all model adapters.
- **Observability & Durability:** Normalized conversation messages, durable additive JSON serialization, reasoning/thinking token capture with provider signatures, and streamable run events.
- **Production Guardrails:** Pre-send token estimation, token-budgeted history truncation, client-side untrusted history sanitization (`SanitizeHistory`), history repair (`NormalizeHistory`), and user-configurable cost/token bounding.
- **Rich Tool Ecosystem:** First-class Model Context Protocol (MCP) client over stdio and HTTP, layout-aware PDF extraction, multi-format doc parsing, sandboxed shell execution, SSR web fetch, and progressive Agent Skills loading.
- **Zero Dependencies:** Built exclusively on the Go standard library.

For architecture rationale, read [the foundation brief](docs/foundation.md). For upcoming milestones, read the [development roadmap](docs/ROADMAP.md).

---

## Development

Run the test suite and verification checks:

```bash
go test ./...
go test -race ./...
go vet ./...
```

The feature guides publish as the official documentation site; preview it locally with `mkdocs serve` (see [docs/website.md](docs/website.md)). Brand assets and guidelines live in [assets/brand/](assets/brand/README.md).

---

## Community & Contributing

- [Contributing Guide](CONTRIBUTING.md) — instructions for setting up, running checks, and submitting changes
- [Security Policy](SECURITY.md) — guidelines for privately reporting vulnerabilities
- [Code of Conduct](CODE_OF_CONDUCT.md) — standards for participation

---

## License

Released under the [MIT License](LICENSE).
