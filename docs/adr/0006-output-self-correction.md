# ADR 0006: Output self-correction

## Status

Accepted.

## Context

When a run's final response fails decoding, the run ends at `StageDecode`.
Real models sometimes produce output a typed decoder must reject: malformed
JSON, a missing field, a constraint violation. Upstream, this is Pydantic
AI's signature capability — validation failures are fed back to the model
("fix the errors and try again") under an explicit retry budget, separate
from transport retries. Without it, Golem's typed-output promise is only
best-effort.

Transport retries (ADR 0004) already classify *provider* failures. This
decision covers *content* failures: the model answered, but the answer is
not acceptable to the application's decoder.

## Decision

### The signal lives at the port

`model.ModelRetry` is an error type in the shared `model` package:

```go
&model.ModelRetry{Err: errors.New("guess must be between 1 and 6")}
```

A decoder returns it to say: reject this response, the model can correct
it if asked. The placement matches `model.RetryableError`: both are
signals consumed by execution policy, visible to every layer (the runner
cannot import `golem`, so the type cannot live there). Placement now also
anticipates tools returning the same signal without a breaking move.

The two signals are deliberately distinct: `ModelRetry` marks content the
model can fix; `RetryableError` marks transport faults worth re-attempting.
`ModelRetry` is never transport-retryable.

### Correction rounds are agent-owned

Decoding happens at the application boundary, after the runner's loop
finishes — that stays. On a `ModelRetry`, the agent appends a user-role
rejection message to the conversation evidence and starts the runner loop
again with the extended messages:

```
[..., assistant(rejected response),
 user("Your previous response was rejected: <reason>. Correct it and respond again."),
 assistant(corrected response)]
```

The rejection is `RoleUser`, not `RoleSystem`: system stays purely for the
agent's instructions (ADR 0005), and user-role retry prompts match
upstream. Each round is a full runner execution with a fresh turn budget;
the worst case is `outputRetries × maxIterations` model turns — bounded by
both.

### The budget is explicit and off by default

`WithOutputRetries(n)` sets the number of correction rounds after the
initial decode; the default is 0, making self-correction opt-in exactly
like transport retries. Validation failures that are not `ModelRetry` —
and every failure while the budget is 0 — end the run exactly as before.

### Evidence stays honest

Rejected responses and their rejection prompts remain in
`Result.Messages`; usage sums across rounds. A caller can see every
correction the model needed. Exhausted budgets wrap the final error with
the attempt count (`output failed validation after N attempts`) at
`StageDecode`; the cause stays reachable through `errors.Is` and
`errors.As`. Wrapping only happens when a correction round actually
occurred, so the default failure shape is unchanged.

## Alternatives considered

- **Tool self-correction in the same change.** Deferred: tool errors
  currently abort the run per ADR 0002, and amending that loop policy
  deserves its own decision when a concrete need exists.
- **Decoding inside the runner.** Rejected: the runner would need the
  agent's decoder (import cycle), and decoding is an application-boundary
  concern, not loop mechanics.
- **System-role rejection messages.** Rejected: muddies ADR 0005's rule
  that system carries instructions only.
- **A default budget greater than zero.** Rejected: correction rounds are
  extra model calls — cost and latency the caller did not ask for.
- **Dropping rejected responses from the evidence.** Rejected: hides the
  model's actual behavior from the caller; honest evidence is a
  foundation rule.
- **A single shared budget with transport retries.** Rejected: transport
  faults and content failures have different causes, different pacing
  needs (backoff vs. none), and different observability.

## Consequences

- Decoders gain a constructive contract: reject what the model can fix,
  fail on what it cannot.
- Applications opt into one more knob and should size it with the turn
  limit in mind.
- The conversation evidence grows with correction rounds; long histories
  may later want trimming (deferred, as in ADR 0005).
- Tools returning `ModelRetry` today still abort the run with a tool-stage
  error; adopting the signal there later is additive.
