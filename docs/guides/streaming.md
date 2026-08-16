# Streaming

## Purpose

Show progress as the model generates: every fragment forwarded the moment
it arrives, across tool turns and correction rounds, while the run still
produces its canonical `Result`.

## When to use

Chat UIs, long generations, anything where silence looks like a hang. Not
when retry resilience matters more than progress — streamed turns are
single-attempt.

## How it works

The port is an optional capability: models that can stream implement
`model.StreamingModel`; `Model` itself is unchanged, so fakes and simple
adapters are unaffected. `GenerateStream` delivers each fragment through
a synchronous callback on the caller's goroutine — no goroutines, no
buffers, no backpressure policy — and returns the fully assembled
`model.Response`, the same shape and normalization `Generate` would
produce.

Agents stream whole runs: `RunStream` (and `RunStreamWithHistory`)
forward every delta — text, tool-call arguments, re-streamed correction
rounds — in arrival order and return the identical `Result`.

## Example

Run `examples/streaming`:

```bash
OPENAI_API_KEY=sk-... go run ./examples/streaming
```

```go
result, err := agent.RunStream(ctx, runCtx, "summarize the match",
    func(d model.Delta) error {
        fmt.Print(d.Content)
        return nil // a non-nil return stops the run
    })
```

## API surface

- `model.StreamingModel` — `GenerateStream(ctx, request, onDelta func(model.Delta) error) (Response, error)`
- `model.Delta{Content string, ToolCalls []ToolCallDelta}`
- `(*Agent).RunStream(ctx, runCtx, prompt, onDelta)`
- `(*Agent).RunStreamWithHistory(ctx, runCtx, history, prompt, onDelta)`

## Gotchas

- The model must implement `model.StreamingModel`; otherwise `RunStream`
  fails up front — no silent fallback to non-streaming generation.
- Streamed turns are single-attempt: a retryable failure ends the run at
  the model stage instead of replaying fragments the caller already saw.
- An error returned from `onDelta` stops the run and comes back at the
  model stage with the original error reachable via `errors.Is`.
- Deltas are in-flight progress, not a persistence contract.
- Decisions live in `docs/adr/0008-streaming-port.md` and
  `docs/adr/0009-agent-streaming-runs.md`.
