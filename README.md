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

Errors are classified by stage (`model`, `tool`, `decode`, `loop`) in `RunError` while preserving the cause for `errors.Is` and `errors.As`. A run makes at most `DefaultMaxIterations` model turns unless `WithMaxIterations` overrides the limit. Messages serialize to stable, additive-only JSON, so applications can persist conversations with `encoding/json`.

## Retrying transient model failures

Retries are opt-in and bounded: `WithMaxAttempts` sets how many times each model call may be attempted, including the first. Only retryable model failures — adapters classify 408, 429, 5xx, and transport faults through `model.RetryableError` — are retried. Tool and decode failures are never retried, and cancellation always wins over any retry.

```go
agent, err := golem.New[struct{}, string](client, decoder,
    golem.WithMaxAttempts[struct{}, string](3),
)
```

Retried attempts wait with exponential backoff (500 ms doubling, capped at 30 s); pass a function to `WithRetryBackoff` to control pacing yourself, including jitter. Exhausted retries fail with the `model` stage, preserving the provider cause for `errors.Is` and `errors.As` and reporting the attempt count in the message.

## Continuing conversations

A run returns the full normalized conversation in `result.Messages`; pass it back to continue. The agent's instructions are re-evaluated every run — system messages from earlier runs are replaced by the current instructions, so guidance never duplicates or goes stale.

```go
result, err := agent.Run(ctx, golem.RunContext[MyDeps]{Deps: deps}, "first question")
next, err := agent.RunWithHistory(ctx, golem.RunContext[MyDeps]{Deps: deps}, result.Messages, "follow-up")
```

Conversations persist anywhere with `encoding/json`: `json.Marshal(result.Messages)` produces the stable, additive-only shape. Storage stays the application's job — there is no session object.

## Self-correcting output

When a typed decoder rejects a response the model can fix, return `model.ModelRetry` from the decoder and enable a correction budget: the run appends the rejection reason to the conversation and asks the model again.

```go
agent, err := golem.New[struct{}, int](client,
    golem.DecodeFunc[int](func(ctx context.Context, r model.Response) (int, error) {
        value, err := strconv.Atoi(strings.TrimSpace(r.Message.Content))
        if err != nil {
            return 0, &model.ModelRetry{Err: fmt.Errorf("answer must be an integer, got %q", r.Message.Content)}
        }
        return value, nil
    }),
    golem.WithOutputRetries[struct{}, int](2),
)
```

Rejected responses and rejection prompts stay in the evidence, and usage sums across rounds. The budget is off by default: without `WithOutputRetries` — or for decode errors that are not `ModelRetry` — the run fails at the `decode` stage exactly as before.

## Self-correcting tools

Tools know their own validation rules. A tool rejects a call the model can fix by returning an error wrapping `model.ModelRetry`; with a budget configured, the run delivers the rejection as that call's tool result so the model can correct its arguments and call again.

```go
roll := tool.MustNew(tool.Tool[MyDeps]{
    Name:        "roll",
    Description: "Roll a die; n must be positive.",
    Schema:      json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer"}}}`),
    Exec: func(ctx context.Context, deps MyDeps, args json.RawMessage) (string, error) {
        var input struct {
            N int `json:"n"`
        }
        if err := json.Unmarshal(args, &input); err != nil {
            return "", err
        }
        if input.N <= 0 {
            return "", &model.ModelRetry{Err: fmt.Errorf("n must be positive, got %d", input.N)}
        }
        return fmt.Sprintf("rolled %d", input.N), nil
    },
})
agent, err := golem.New[MyDeps, string](client, decoder,
    golem.WithTools[MyDeps, string](roll),
    golem.WithToolRetries[MyDeps, string](2),
)
```

Rejected calls and their rejection results stay in the evidence. The budget is off by default: without `WithToolRetries` — or for tool errors that are not `ModelRetry` — the run still aborts at the `tool` stage with the cause preserved.

## Streaming

Models that can stream advertise it through the optional `model.StreamingModel` capability — `Model` itself is unchanged, so test fakes and simple adapters are unaffected. The OpenAI-compatible adapter implements it over SSE:

```go
response, err := client.GenerateStream(ctx, request, func(d model.Delta) error {
    fmt.Print(d.Content)
    return nil // a non-nil return stops the stream and comes back as-is
})
```

`GenerateStream` returns the fully assembled `model.Response` — the same shape and normalization as `Generate` — so evidence, tools, and decoding keep operating on the canonical response. Streamed usage depends on provider support (`stream_options.include_usage`); providers that omit it report zeroes.

Agents stream whole runs: `RunStream` (and `RunStreamWithHistory`) forward every fragment across tool turns and correction rounds while producing the identical `Result`.

```go
result, err := agent.RunStream(ctx, golem.RunContext[MyDeps]{Deps: deps}, "summarize the match",
    func(d model.Delta) error {
        fmt.Print(d.Content)
        return nil
    },
)
```

The model must implement `model.StreamingModel` — otherwise the call fails up front rather than silently degrading. Streamed turns are single-attempt: a retryable failure ends the run at the model stage instead of replaying fragments the caller already saw; use non-streaming runs when retry resilience matters more than progress.

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
