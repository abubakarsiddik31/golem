# ADR 0047: Opt-in content-filter failure

Date: 2026-11-15
Status: Accepted

## Context

Every adapter normalizes a provider safety stop to
`model.FinishContentFilter`, and `Result.FinishReason` already reports
it. The response is otherwise treated as a normal answer: its text —
empty, refusal, or partial — reaches the decoder. That matches v1 and
avoids discarding text a provider chose to return.

Pydantic AI's default raises a content-filter error only when the
response is empty, with a capability that extends the failure to every
`content_filter` response. Golem has no typed content-filter failure,
so applications that must distinguish refusal from an answer have to
inspect `FinishReason` after a successful run and reconstruct the
failure themselves.

Changing the default would alter existing runs and the v1 contract.
The feature therefore has to be opt-in.

## Decision

`WithContentFilterError()` makes any response whose finish reason is
`FinishContentFilter` fail the run, before output decoding and before
tool dispatch, even when refusal or partial text is present. The run
returns `RunError{Stage: StageContentFilter}` wrapping a
`*ContentFilterError`, which preserves the filtered `model.Message` on
`Message`; `RunError.Partial` also carries the response as evidence.

Without the option behavior is unchanged: the response is ordinary
output and `Result.FinishReason` reports the cause. The zero value is
the v1 contract.

The check lives in the runner's shared loop, gated by
`ToolConfig.RejectContentFilter`, so plain and streamed runs stop at
the same point and a filtered response containing a tool call cannot
execute it. The agent maps the runner's sentinel to the public typed
error.

## Alternatives considered

- **Always fail on content_filter**: changes existing behavior and
  discards refusal text some applications want. Rejected for v1.
- **Fail only when the response is empty** (Pydantic's default): adds a
  second implicit policy and still cannot express "every filtered
  response fails". The finish reason already tells the caller the
  response was empty.
- **Reuse `StagePolicy`**: that stage means an application guard blocked
  the output. A provider filter is a different cause with different
  remediation; a distinct stage keeps `RunError` inspectable.
- **Return the error from the decoder**: decoders are caller-supplied
  and optional in tool-output mode; the loop must stop before tools
  regardless of decoding.
- **A provider-specific error**: adapters already normalize the finish
  reason, so the core can make the policy provider-neutral.

## Consequences

- New exported names: `WithContentFilterError`, `ContentFilterError`,
  and the `StageContentFilter` stage value; all additive.
- `runner.ToolConfig` gains one field. The filtered response is kept on
  the outcome even though the run fails, so partial evidence is exact.
- Content-filter failures are terminal: they are not retried and do not
  trigger model fallback. The guide states this and suggests an explicit
  fallback run.
- Tests cover the typed failure, evidence, the default non-failing
  behavior, empty and partial responses, and the guarantee that a
  filtered tool call never executes.
