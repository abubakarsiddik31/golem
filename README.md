# Golem

Golem is a Go-first framework for building dependable AI agents: typed dependencies and outputs, explicit tools, composable models, and an observable execution loop.

It takes inspiration from the ergonomics of Python agent frameworks such as Pydantic AI, but follows Go's strengths instead: compile-time contracts, `context.Context`, explicit error handling, small interfaces, and standard-library-friendly integrations.

## Status

The project is at the foundation stage. The public API is intentionally small while the core execution contract is established.

## Direction

- Make a useful agent the shortest path: configure a model, declare tools, run with dependencies, get a typed result.
- Make important behavior explicit: model calls, tool execution, iteration limits, usage, and validation are visible in the run result.
- Keep infrastructure replaceable: applications choose models, tracing, storage, and transport through narrow interfaces.
- Prefer Go-native composition over ports of Python metaprogramming.

Read [the foundation brief](docs/foundation.md) before proposing a new public abstraction. Contributor and coding-agent rules live in [AGENTS.md](AGENTS.md).

## Declaring and running tools

Tools are typed values: inspectable metadata plus an execution function that receives the caller's context, the run's dependency value, and raw model-produced arguments. A model's tool request is executed sequentially and every exchange is preserved in the result.

```go
getPlayerName := tool.MustNew(tool.Tool[string]{
    Name:        "get_player_name",
    Description: "Get the player's name.",
    Schema:      json.RawMessage(`{"type":"object"}`),
    Exec: func(ctx context.Context, playerName string, args json.RawMessage) (string, error) {
        return playerName, nil
    },
})

agent, err := golem.New[string, string](modelClient, decoder,
    golem.WithTools[string, string](getPlayerName),
)
result, err := agent.Run(ctx, golem.RunContext[string]{Deps: "Anne"}, "My guess is 4")
```

Errors are classified by stage (`model`, `tool`, `decode`, `loop`) in `RunError` while preserving the cause for `errors.Is` and `errors.As`. A run makes at most `DefaultMaxIterations` model turns unless `WithMaxIterations` overrides the limit.

## Connecting a provider

Golem's core is provider-neutral: applications choose any implementation of `model.Model`. The first adapter targets the OpenAI-compatible chat-completions wire format with explicit configuration — a configurable `BaseURL` serves OpenAI, Groq, OpenRouter, DeepSeek, Together, Ollama, and vLLM. The adapter uses only the standard library.

```go
client, err := openai.New(openai.Config{
    APIKey: os.Getenv("OPENAI_API_KEY"),
    Model:  "gpt-4o",
})
agent, err := golem.New[struct{}, string](client, decoder)
```

Provider failures return a typed `openai.APIError` and network-level failures a `TransportError`. Both expose their retryability through `model.RetryableError` (408, 429, 5xx, transport faults — never context cancellation) for the runner's retry policy; the adapter never retries on its own. Environment variables are read by the application, never implicitly by Golem.

## Planned package shape

```text
golem/        Agent configuration and typed run API
model/        Provider-neutral model request/response contract
tool/         Tool declarations and execution contracts
providers/    Stdlib-only adapters implementing model.Model
internal/     Execution loop and non-public mechanics
docs/adr/     Decisions that shape the public contracts
```

## Development

```bash
go test ./...
go vet ./...
```

The module path is provisional until the private GitHub repository is created.
