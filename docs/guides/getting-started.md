# Getting started

## Purpose

Run the smallest useful agent — a model, a decoder, one prompt — and know
where every capability lives in the package layout.

## When to use

Start here for a first run or a new project. Every other guide assumes
this setup.

## How it works

An agent is built once with `golem.New[Deps, Output]` and run many times.
Two collaborators are required: a `model.Model` (usually a provider
adapter) and an `OutputDecoder[Output]` — the boundary where untrusted
model output becomes an application value. Everything else —
instructions, tools, budgets, schemas — is an option function.

A run returns a `Result[Output]` carrying the typed output, the full
normalized conversation in `Messages`, and cumulative `Usage`. Errors are
`RunError` values with an inspectable stage — compare against
`golem.StageModel`, `golem.StageTool`, `golem.StageDecode`,
`golem.StageLoop`, or `golem.StageUsage` — that preserve the cause for
`errors.Is` and `errors.As`. A run that had begun producing evidence —
completed model turns, reported usage, executed tools — carries it as
`RunError.Partial`: the conversation through the last completed model
turn, the usage completed turns reported, and the counts of model
requests and tool executions. `Partial` is nil when the run failed
before any of that; feed `Partial.Messages` to `RunWithHistory` to
resume a failed conversation.

## Example

Run `examples/minimal`:

```bash
OPENAI_API_KEY=sk-... go run ./examples/minimal
```

```go
client, _ := openai.New(openai.Config{APIKey: apiKey, Model: "gpt-4o-mini"})
agent, _ := golem.New[struct{}, string](client,
    golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
        return r.Message.Content, nil
    }),
)
result, err := agent.Run(ctx, golem.RunContext[struct{}]{}, "Reply with exactly the word: pong")
```

## API surface

- `golem.New[Deps, Output](model, decoder, options ...Option[Deps, Output]) (*Agent[Deps, Output], error)`
- `(*Agent).Run(ctx, runCtx, prompt) (Result[Output], error)`
- `golem.RunContext[Deps]{Deps: value}`
- `golem.DecodeFunc[Output](func(ctx, model.Response) (Output, error))`

## Gotchas

- Runs make at most `golem.DefaultMaxIterations` (10) model turns; see
  `WithMaxIterations`.
- Cancellation and deadline errors ride a `RunError` like every other
  failure and stay matchable with `errors.Is(err, context.Canceled)`
  through the chain — wrapping is what preserves their partial evidence.
- Environment variables are read by the application, never by Golem.
