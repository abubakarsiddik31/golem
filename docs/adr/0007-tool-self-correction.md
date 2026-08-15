# ADR 0007: Tool self-correction

## Status

Accepted.

## Context

ADR 0002 §Loop policy made every tool error fatal: a failing tool ends the
run with a tool-stage error preserving the cause. That rule keeps failures
explicit, but it cannot distinguish two very different situations:

- The tool genuinely failed — a database is down, a precondition the model
  cannot know was violated.
- The model sent arguments the tool's own validation rejects — a negative
  ID, a missing field, a constraint only the tool author knows.

The second case is content the model can fix if told why, exactly like a
decoder rejecting a final response. Upstream, Pydantic AI lets tools raise
`ModelRetry` for this. Golem already shipped `model.ModelRetry` for
decoders (ADR 0006) and placed it in the shared `model` package precisely
so tools could adopt the same signal without a breaking move; ADR 0006
deferred this decision.

This amends ADR 0002 §Loop policy ("abort on tool error"), the same way
ADR 0004 amended ADR 0003.

## Decision

### The signal is the existing `model.ModelRetry`

A tool rejects a call as correctable by returning an error wrapping it:

```go
return "", &model.ModelRetry{Err: fmt.Errorf("player_id must be positive, got %d", id)}
```

No new type is introduced. The runner detects the signal with
`errors.As`; the `tool` package itself gains no imports — tool authors
import `model` in their own code, which the package graph always allowed.
`ModelRetry` stays non-transport-retryable: a rejected tool call is never
silently re-executed, it is reported to the model.

### Correction is runner-owned, not agent-owned

Output correction (ADR 0006) is agent-owned because decoding happens after
the loop finishes. A tool rejection is the opposite: it occurs *inside*
the loop, and the correction must reach the model as a tool result so the
same conversation can continue. The runner appends a `RoleTool` message
for the rejected call — same shape as a normal result, carrying the call's
`ToolCallID` and `ToolName` — with content:

```
Your tool call was rejected: <reason>. Correct the arguments and call the tool again.
```

The assistant's tool-call message stays in the evidence, remaining calls
in the same batch still execute, and the loop returns to the model.

### The budget is explicit and off by default

`WithToolRetries(n)` sets how many tool rejections a run feeds back, in
total across all tools; the default is 0, making correction opt-in exactly
like transport and output retries. Negative values fail construction.
Unknown-tool requests and tool errors that do not carry `ModelRetry`
remain fatal regardless of budget: the first is a configuration error,
the second an application failure the model cannot fix.

When the budget is exhausted, the run ends with a `ToolError` at the tool
stage; if a feedback actually happened, the error is wrapped with the
attempt count (`tool failed after N attempts`) so the shape matches
exhausted model and output retries. The `ModelRetry` and its cause stay
reachable through `errors.Is` and `errors.As`. With budget 0 the failure
is byte-for-byte today's behavior.

### Evidence stays honest

Rejected calls and their rejection results remain in `Result.Messages`;
usage sums across turns. The budget is per loop execution, so an
output-retry round gets a fresh tool budget, just as it gets a fresh turn
budget. The worst case is bounded by both budgets and `maxIterations`.

## Alternatives considered

- **Feeding back all tool errors.** Rejected: it hides real failures from
  the application by default, contradicting the foundation's
  explicitness rule. The `ModelRetry` signal keeps the decision with the
  tool author.
- **Per-tool-name budgets.** Deferred: more precise, but needs per-run
  state keyed by tool and has no consumer yet; a run-wide total is the
  smallest auditable unit.
- **Agent-owned correction via loop re-entry.** Rejected: the loop is the
  runner's job; re-entering it from the agent to deliver a tool result
  would duplicate loop machinery and complicate evidence ordering.
- **Sharing the output-retry budget.** Rejected: decoder rejections and
  tool rejections have different owners, different delivery shapes, and
  different observability needs.
- **Correctable unknown-tool requests.** Rejected: requesting an
  undeclared tool is a wiring mistake, not model content; telling the
  model to retry would mask it.

## Consequences

- Tools gain a constructive contract: reject what the model can fix,
  fail on what it cannot.
- A model that never corrects still terminates: each fed-back rejection
  consumes a model turn, so `maxIterations` bounds the run.
- The runner's `Execute` signature gains the budget parameter; it is
  internal, so no public surface moves.
- Evidence grows with rejected calls; callers can audit every correction
  the model needed, as with output retries.
