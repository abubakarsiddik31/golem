# ADR 0004: Runner retry policy

## Status

Accepted.

## Context

ADR 0002 reserved retries as explicit runner policy: models and tools return
classified errors and never retry themselves. ADR 0003 then gave adapters a
retryability classification (`Retryable` for 408, 429, and 5xx), but nothing
consumes it yet — a single transient 429 still fails the whole run.

The import graph constrains the design. The runner (`internal/runner`) must
never import `providers/<name>`; only the `model` port package is shared by
the core and every adapter. The retryability signal therefore has to travel
through the port.

## Decision

### Retryability contract at the port

`model` defines the classification contract:

```go
type RetryableError interface {
    error
    Retryable() bool
}

func IsRetryable(err error) bool
```

Adapters satisfy the interface structurally; the runner inspects error chains
with `model.IsRetryable`. Context cancellation and deadline errors are never
retryable, whatever else the chain contains.

This amends ADR 0003 §Error classification: adapters expose retryability
through the `model.RetryableError` method rather than a struct field
(`APIError.Retryable` becomes an accessor). Network-level failures get the
same treatment: the adapter wraps them in a transport error that is retryable
unless the cause is a context error.

### What retries

- **Model calls only.** Each turn's `Generate` may be retried up to a bounded
  attempt count. The iteration limit counts model turns, not attempts, and
  failed attempts append no messages and record no usage.
- **Tools never retry.** Tool execution belongs to the application; replaying
  side effects is not the framework's decision (extends ADR 0002's abort
  policy).
- **Decoding never retries.** A decode failure is deterministic.

### Policy shape

- **Attempts are opt-in.** The default is one attempt: byte-for-byte the
  pre-retry behavior. Retrying by default would silently multiply cost and
  latency on error paths, which contradicts the foundation's explicitness
  rule. Opting in is one constructor option.
- **Backoff is injectable** as a function of the 1-based failed attempt
  number, so tests run without a clock. When attempts are enabled without an
  explicit backoff, the core applies exponential backoff (500 ms doubling,
  capped at 30 s).
- **Waits honor the caller's context** via a timer and select — no
  goroutines are introduced. Cancellation during a wait returns the raw
  context error, preserving the existing unwrapped-cancellation contract.

### Terminal reporting

An exhausted or non-retryable model failure returns the terminal cause
wrapped with the attempt count (`model failed after N attempts`) — but only
when a retry actually happened; single-attempt failures return the error
unchanged. The cause stays reachable through `errors.Is` and `errors.As`, and
the run classifies it as `StageModel`. There is deliberately no exported
retry error type: no caller has a need that `RunError` plus the preserved
cause does not cover.

## Alternatives considered

- **Retry tool executions** with the same policy. Rejected: tools carry
  application side effects; replaying them is the application's decision.
- **Retry by default** (e.g. three attempts). Rejected: silent cost and
  latency multiplication on error paths; explicit opt-in fits the
  foundation's rules.
- **A retry library** (backoff strategies, jitter). Rejected: the repository
  carries zero third-party dependencies, and a bounded loop with an
  injectable backoff function is small.
- **An exported `RetryError` type** carrying attempts. Rejected for now: a
  new exported concept needs a representative caller; the wrapped terminal
  cause already answers `errors.Is`/`errors.As`.
- **Retry classification in the runner** (status-code tables per provider).
  Rejected: the runner would then know about providers, inverting ADR 0003.

## Consequences

- Adapters classify; the runner decides. Adding a provider means
  implementing one method, not teaching the core new failure shapes.
- Default-off keeps existing error behavior and tests exactly as they were.
- The backoff contract is a plain function, so applications can add jitter
  or metrics without framework support.
