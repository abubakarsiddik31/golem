# ADR 0018: Deferred tools — approval and external execution

## Status

Accepted.

## Context

Golem's runner executes every requested tool call inline: a call's
`Exec` runs, its result joins the conversation, and the loop continues.
That leaves two real scenarios unexpressible. A tool that performs a
consequential action — the shipped `shell` and `fileread` common tools
are exactly this — has no way to stop and wait for a human sign-off
before the action happens. And a tool whose result is produced outside
the agent process — by a frontend, another service, or a long job —
has no way to hand the conversation to whoever holds the result.

The signal can only come from the tool itself: whether a call needs
approval depends on its arguments and the run's dependencies, which
only `Exec` sees. Today a tool's only options are to fake a result
(lying to the model) or to fail the run (losing the conversation).

Prior art splits along one line. Pydantic AI V2 and the Microsoft
Agent Framework pause the run out-of-band: pending requests surface to
the application, and a later run resumes the same conversation with
the resolutions. Google's ADK Go instead mediates through the model —
it re-emits the call wrapped in a synthetic `adk_request_confirmation`
function call and waits for the model to relay the answer. The
model-mediated path spends a model turn per approval, makes the
confirmation dance visible on the wire, and inherits Gemini's
function-call heritage; Golem stays with the conversation-level pause.

Provider risk is nil by construction. Every provider requires
call/result pairing before the next model call (the invariant ADR 0005
encodes as history repair), and a paused run makes no model call
between pause and resume — the resume request is the first one that
sees the deferred calls, and by then every call is answered.

## Decision

A tool defers by returning an error, the same signal channel as
`model.ModelRetry`.

`tool.Deferred` is the sentinel: `Kind` is `DeferApproval` (a human
must approve or deny the call) or `DeferExternal` (the application
supplies the result), and `Reason` carries the tool's explanation to
whoever resolves the request. Approved re-executions are visible to
the tool through `tool.CallApproved(ctx)` — a context marker the
runtime sets — so a tool gates its side effects behind the flag and
the same `Exec` serves both passes. No `Exec` signature changes.

The run pauses instead of failing. When any call in a batch defers,
the other calls execute normally, their results join the evidence, and
the loop ends with `Outcome.Pending` — the deferred calls with their
sentinels, in emission order. Deferred calls are not tool executions
for usage accounting; their approved re-runs on resume are. The agent
surfaces this as `Result.Pending` (grouped `Approvals` and `External`,
each a `PendingToolCall` with call ID, name, arguments, and reason);
`Output` stays the zero value, and decoding is skipped — a pause has
no final answer to validate. A `deferred` run event fires per pending
call, so stream consumers and observers see the pause point.

Resume is a new run: `RunWithDeferredResults` takes the paused run's
history and a `DeferredResults` value keyed by call ID. Validation
runs before any model call: the history must end with pending deferred
calls, every pending call must be resolved exactly once, and no
unknown call ID is accepted. Resolutions become the calls' tool-result
messages in emission order — an approved call re-executes through the
same timeout path with the approved marker set, a denial becomes a
denial message the model cannot mistake for a result, and an external
result is handed to the model verbatim. An optional new prompt joins
the conversation after the resolutions; an empty prompt adds no user
message. From there the run continues through the ordinary loop, and a
resume may itself pause again.

The re-execution failure policy is strict: an approved re-run that
errors fails the resume run at the tool stage, and a re-run that
defers again is a tool bug and fails the same way. No correction
budget applies to re-runs in this iteration.

## Alternatives considered

- **Model-mediated confirmation (ADK Go's `adk_request_confirmation`).**
  Rejected: an extra model turn per approval, confirmation visible on
  the wire, and the model can paraphrase or drop the decision.
- **Pause as a `RunError` stage.** Rejected: a pause is a successful
  outcome the application acts on, not a failure; every run stage
  today is a failure classification, and an error would push callers
  into error handling for the normal path.
- **Union output type (upstream's `DeferredToolRequests` as
  `output_type`).** Rejected: Go generics make a union output type
  awkward, and the information is not an answer — `Result.Pending`
  keeps the typed output contract untouched.
- **Result injection only, no re-execution.** Rejected for approvals:
  the approval gate must gate the action, which lives inside the tool.
  Injection-only would force the caller to perform the tool's action
  out-of-band, defeating the gate. (For external results, injection is
  the whole mechanism, and that is what ships.)
- **A static `RequiresApproval` field on `tool.Tool`.** Rejected: one
  mechanism — the sentinel — covers both the static case (defer
  unconditionally at the top of `Exec`) and the conditional case
  (defer after inspecting arguments), without widening the
  declaration surface.
- **Upstream's inline-handler flow** (a resolver callback that
  resolves some calls inside the same run). Rejected for now: Golem's
  synchronous `Exec` already lets an in-process resolver block on an
  approval channel inside the tool and return normally — the flow is
  expressible today without framework support.

## Consequences

- Paused evidence ends with unanswered tool calls. Feeding that
  history to plain `RunWithHistory` synthesizes interrupted results
  for them (existing repair behavior); the guide says to resume with
  `RunWithDeferredResults` instead.
- No model, wire, or persisted-JSON change: pending calls are
  `model.ToolCall` values already in history, and resolutions are
  ordinary tool-result messages. Providers need nothing.
- `tool` gains `Deferred`, `DeferApproval`, `DeferExternal`,
  `CallApproved`, and the runtime-facing `WithApprovedCall`; the root
  package gains the pause/resume types and method; `internal/runner`
  gains the pending outcome, the deferred event, and the approved
  re-run entry point.
- Approval is a model-boundary control, not an authorization
  boundary: whoever submits history for resume submits the approvals
  too. Applications must authenticate the resume path themselves; the
  guide says so.
- A paused run persists like any run — history JSON is unchanged — so
  pause/resume can span processes, with the application holding the
  history and the resolutions between the two runs.
