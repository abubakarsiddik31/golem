# Self-correction

## Purpose

Let the model fix its own mistakes: responses and tool calls the
application rejects come back to the model as correction feedback, under
an explicit budget.

## When to use

When validation failures are model-fixable — wrong shape, out-of-range
arguments, policy violations the model can understand. Not for failures
the application must handle itself.

## How it works

Two independent, opt-in budgets:

- **Output.** A decoder rejects a response by returning an error wrapping
  `*model.ModelRetry`. The run keeps the rejected response as evidence,
  appends the rejection reason as a user message, and asks the model
  again — up to `WithOutputRetries` rounds. Usage sums across rounds.
- **Tools.** A tool rejects correctable arguments by returning an error
  wrapping `*model.ModelRetry`. The run delivers the rejection as that
  call's tool result so the model can correct its arguments and call
  again — up to `WithToolRetries` rejections per run, bounded also by the
  model-turn limit.

Without a budget — or for errors that are not `ModelRetry` — the run
fails at the `decode` or `tool` stage exactly as before, with the cause
preserved.

## Example

Run `examples/self-correction` for the tool path; `ExampleWithOutputRetries`
and `ExampleWithToolRetries` in package docs cover both:

```bash
OPENAI_API_KEY=sk-... go run ./examples/self-correction
```

```go
if input.N <= 0 {
    return "", &model.ModelRetry{Err: fmt.Errorf("n must be positive, got %d", input.N)}
}
```

## API surface

- `golem.WithOutputRetries[Deps, Output](retries int)`
- `golem.WithToolRetries[Deps, Output](retries int)`
- `model.ModelRetry{Err: error}` — the correction signal.

## Gotchas

- Budgets are off by default; negative values fail construction.
- The tool budget counts total rejections per run, not per tool.
- Rejected exchanges stay in `result.Messages` — evidence grows with
  each round.
- Decisions live in `docs/adr/0006-output-self-correction.md` and
  `docs/adr/0007-tool-self-correction.md`.
