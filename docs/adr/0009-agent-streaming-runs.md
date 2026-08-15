# ADR 0009: Streaming agent runs

## Status

Accepted.

## Context

ADR 0008 shipped the streaming port (`model.StreamingModel`, callback
delivery, adapter-owned accumulation) and the OpenAI-compatible SSE
adapter. But agents cannot use it: `Agent.Run` drives the loop through
`runner.Execute`, which calls `Generate`. Applications need fragments
while a *full run* is in flight — across tool-call turns, tool
rejections, and output-correction rounds. This is the integration ADR
0008 deferred to its own decision.

## Decision

### Streaming runs mirror the existing run pair

```go
func (a *Agent[Deps, Output]) RunStream(ctx context.Context, runCtx RunContext[Deps], prompt string, onDelta func(model.Delta) error) (Result[Output], error)
func (a *Agent[Deps, Output]) RunStreamWithHistory(ctx context.Context, runCtx RunContext[Deps], history []model.Message, prompt string, onDelta func(model.Delta) error) (Result[Output], error)
```

Evidence, usage, error stages, and every correction budget are
unchanged: deltas are advisory progress on top of the canonical run,
and the returned `Result` is identical in shape and normalization to a
non-streaming run's.

### The callback forwards every fragment from every model turn

Tool-call argument fragments, text streamed between tool executions,
and re-streamed rounds after a tool rejection (ADR 0007) or an
output-rejection correction (ADR 0006) all flow to `onDelta` in order.
Locally produced messages — tool results, rejection prompts — are not
streamed: they are not model fragments, and they exist in full the
moment they are created.

### Capability mismatch fails up front, plainly

If the model does not implement `model.StreamingModel`, `RunStream*`
returns an unwrapped configuration error before anything runs — the
same class as `New`'s validation errors, not a `RunError`, because no
stage executed. There is deliberately no fallback to `Generate`: a
silent fallback would hand the caller a non-streaming run while the
code asked for streaming, hiding the capability loss. Direct callers
get the same guard from `runner.ExecuteStream`.

### Streamed turns are single-attempt

ADR 0004's attempt budget applies to `Generate`; streamed turns are one
attempt. A retried stream replays fragments the caller has already
seen — a terminal consumer would print the text twice with no way to
detect the seam — so a retryable stream failure fails the run at the
model stage, classified, instead of being retried. Callers who want
retry resilience use non-streaming runs; streaming trades it for
immediate progress.

### Caller-stop surfaces at the model stage

An error returned from `onDelta` aborts the stream (the port returns it
as-is), ends the run, and surfaces wrapped in `RunError` at the model
stage with the original error reachable through `errors.Is`: every
error at the model-call boundary is model-stage, and cancellation keeps
its existing unwrapped handling. A nil `onDelta` is allowed and simply
discards fragments.

### Runner mechanics

The runner's loop body moves into a private `execute` that takes the
per-turn generate function as a parameter. `Execute` delegates with the
retrying `generate` (signature unchanged); `ExecuteStream` asserts the
streaming capability and delegates with a closure over
`GenerateStream`. The loop itself — turn limit, tool execution,
rejection feedback, evidence order — is shared, so streaming and
non-streaming runs cannot drift apart.

## Alternatives considered

- **Silent fallback to `Generate` for non-streaming models.** Rejected:
  hides the capability loss from the caller (explicitness rule).
- **Retrying streamed turns within the attempt budget.** Rejected:
  fragment replay duplicates consumer output; partial-progress retries
  need consumer-visible resync semantics nobody has asked to design.
- **Streaming locally produced messages.** Rejected: they are not model
  fragments and would blur the port's meaning for zero benefit.
- **A channel or queue between runner and caller.** Rejected:
  reintroduces the goroutine lifecycle ADR 0008 rejected, for no gain.
- **A separate `StreamingAgent` type.** Rejected: fragments the API for
  one method-level difference; methods mirror the existing pair.

## Consequences

- Streaming runs trade transport-retry resilience for progress; the
  trade is documented and pinned by tests.
- The capability assertion is the caller's checkpoint: compile-time
  guarantees are impossible for a runtime capability, so the failure is
  loud and early.
- The shared `execute` keeps one loop; any future loop policy applies
  to both paths automatically.
- Future hook-style extensions (observability, cancellation hooks) can
  reuse the same callback slot.
