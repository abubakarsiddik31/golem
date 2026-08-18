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

### Falling back to another model

Retrying waits out a failure; a fallback moves past it.
`model.NewFallback(primary, alternates...)` composes models that try in
order: the first success answers, a non-retryable failure returns
immediately unchanged, and when every model fails the last error returns
with its classification intact, so the run's own retry policy still
applies. Cancellation always wins over moving on. Skipped failures are
not surfaced — only the answer or the final error is.

Streams follow one rule: a fallback happens only before the first
forwarded fragment. After any fragment was delivered, falling back would
replay what the caller already saw, so the error returns as-is. Every
member must support streaming; one that does not is a configuration
error, not a fallback candidate.

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

`examples/fallback` chains a primary model with a backup and caps the
run's requests; set `OPENAI_API_KEY` (and optionally `OPENAI_MODEL` and
`OPENAI_FALLBACK_MODEL`) to run it:

```go
fallback, _ := model.NewFallback(primary, backup)
agent, _ := golem.New[struct{}, string](fallback, decoder)
```

## API surface

- `golem.WithMaxAttempts[Deps, Output](attempts int)`
- `golem.WithRetryBackoff[Deps, Output](backoff func(attempt int) time.Duration)`
- `model.IsRetryable(err) bool`
- `model.NewFallback(primary model.Model, alternates ...model.Model) (*model.Fallback, error)`

## Gotchas

- Values below 1 fail construction; the default is 1 (no retries).
- Streamed model turns are single-attempt — replaying fragments a caller
  already saw would be wrong; see [Streaming](streaming.md). A
  `model.Fallback` honors the same rule between its members.
- Decisions live in `docs/adr/0004-runner-retry-policy.md`.
