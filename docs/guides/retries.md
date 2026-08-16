# Retries

## Purpose

Survive transient model failures — 408, 429, 5xx, transport faults —
with an explicit, bounded retry policy.

## When to use

Production runs against live providers. Retries are opt-in; the default
is one attempt per model call.

## How it works

`WithMaxAttempts` bounds how many times each model call may be
attempted, including the first. Only errors that classify as retryable
through `model.RetryableError` are retried — adapters classify provider
statuses, and the adapter itself never retries. Tool and decode failures
are never retried here: tool rejections have their own budget, and decode
failures are model-fixable, not transport faults.

Retried attempts wait with exponential backoff — 500 ms doubling, capped
at 30 s — unless `WithRetryBackoff` supplies its own pacing (including
jitter). Cancellation always wins over any retry. Exhausted retries fail
at the `model` stage, preserving the provider cause for `errors.Is` and
`errors.As` and reporting the attempt count in the message.

## Example

See `ExampleWithOutputRetries` for budgeted correction; retries compose:

```go
agent, _ := golem.New[struct{}, string](client, decoder,
    golem.WithMaxAttempts[struct{}, string](3),
    golem.WithRetryBackoff[struct{}, string](func(attempt int) time.Duration {
        return time.Duration(attempt) * time.Second
    }),
)
```

## API surface

- `golem.WithMaxAttempts[Deps, Output](attempts int)`
- `golem.WithRetryBackoff[Deps, Output](backoff func(attempt int) time.Duration)`
- `model.IsRetryable(err) bool`

## Gotchas

- Values below 1 fail construction; the default is 1 (no retries).
- Streamed model turns are single-attempt — replaying fragments a caller
  already saw would be wrong; see [Streaming](streaming.md).
- Decisions live in `docs/adr/0004-runner-retry-policy.md`.
