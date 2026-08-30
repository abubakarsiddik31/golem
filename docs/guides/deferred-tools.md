# Deferred tools

## Purpose

Let a tool pause the run instead of producing a result: a consequential
action waits for a human decision, and a call whose result lives outside
the process hands the conversation to whoever holds it. Resuming is an
ordinary run over the same history.

## When to use

Use deferred tools when only the tool's execution knows a call cannot or
should not complete now: deleting a file needs sign-off, a payment needs
a human, a report is building on a worker, or the result must come from
a frontend. The signal is per-call and conditional — the same tool can
execute some calls and defer others based on arguments or dependencies.

When a resolver already lives in the same process, no framework support
is needed: a tool can block on an approval channel inside `Exec` and
return normally. Reach for deferral when the resolver is a human, a
separate service, or a later point in time.

## How it works

A tool defers by returning an error that wraps the sentinel
`*tool.Deferred`, choosing a kind:

- `tool.DeferApproval` — the call waits for a human decision. Gate the
  tool's side effects behind `tool.CallApproved(ctx)`: the deferred pass
  and the approved re-run share one `Exec`, and the action must happen
  only on the approved pass.
- `tool.DeferExternal` — the application supplies the result later. The
  `Reason` typically carries a correlation key for the pending job.

When any call in a batch defers, the run pauses cleanly:

- The other calls of the batch execute normally, and their results join
  the evidence. The deferred call gets none.
- `Run` (and every variant, streaming included) returns success with
  `Result.Pending` set — approvals and externals grouped separately,
  each with call ID, tool name, arguments, and the tool's reason. No
  model call follows the pause; check `Pending` before `Output`, which
  stays the zero value because there is nothing to decode.
- A `deferred` run event fires for each pending call; no tool-end event
  follows, and deferred calls do not count toward tool-execution limits.

Resume with `RunWithDeferredResults`, passing the paused run's
`Result.Messages` and a `golem.DeferredResults` keyed by call ID:

- **Approved** — the tool re-executes under the configured timeout with
  the approved marker set, and its return value becomes the call's tool
  result. A re-run that fails, or defers again, fails the resume run at
  the tool stage.
- **Denied** — no re-execution; the model receives a denial message
  stating the decision and the optional reason.
- **External** — the provided text becomes the call's tool result,
  verbatim.

Resolutions join the evidence in the model's emission order, an optional
new prompt follows them, and the run continues through the ordinary
loop — it may pause again. Validation (every pending call resolved
exactly once, no unknown call IDs) fails the resume before any model
call. An empty prompt resumes on the resolutions alone, adding no user
message.

Cancellation during an approved re-run surfaces as the context error,
not a tool error, matching every other stage.

## Example

`examples/deferred-tools` runs the whole cycle offline against a
scripted model — a gated shell-style tool defers, the application
denies one call and approves another, and the resumed conversation
answers. Run it with:

```sh
go run ./examples/deferred-tools
```

The compressed shape:

```go
Exec: func(ctx context.Context, deps Deps, args json.RawMessage) (string, error) {
    if !tool.CallApproved(ctx) {
        return "", &tool.Deferred{Kind: tool.DeferApproval, Reason: "deletes need sign-off"}
    }
    return doDelete(args)
},

result, err := agent.Run(ctx, runCtx, "clean up the workspace")
// result.Pending.Approvals -> ask the human
resumed, err := agent.RunWithDeferredResults(ctx, runCtx, result.Messages,
    golem.DeferredResults{Approvals: map[string]golem.Approval{
        "call-1": {Approved: true},
    }})
```

## API surface

- `tool.Deferred{Kind DeferKind, Reason string}` — the sentinel a tool returns to pause the run.
- `tool.DeferApproval`, `tool.DeferExternal` — the deferral kinds.
- `tool.CallApproved(ctx context.Context) bool` — true on the approved re-run of a deferred call.
- `tool.WithApprovedCall(ctx context.Context) context.Context` — mark a context as the approved re-run; execute a pending call's tool yourself, outside `RunWithDeferredResults`, under your own timeout or retry policy.
- `golem.Result.Pending *DeferredRequests` — the pending calls of a paused run, or nil.
- `golem.DeferredRequests{Approvals, External []PendingToolCall}` — pending calls grouped by resolution kind.
- `golem.PendingToolCall{CallID, ToolName string, Args json.RawMessage, Reason string}` — one pending call.
- `golem.DeferredResults{Approvals map[string]Approval, External map[string]string}` — resolutions by call ID.
- `golem.Approval{Approved bool, Reason string}` — the decision on one approval request.
- `golem.Agent.RunWithDeferredResults(ctx, runCtx, history, results, prompt, opts...)` — resume a paused run.
- `golem.EventDeferred` — the run event emitted per pending call.

## Gotchas

- **The deferred pass already ran your tool.** `Exec` executes and
  returns the sentinel, so everything before the deferral check must be
  safe to repeat. Put side effects after the `tool.CallApproved` check.
- **Approval is not an authorization boundary.** It gates the model
  against acting without human sign-off. Whoever can submit history to
  `RunWithDeferredResults` can submit the approvals too — authenticate
  the resume path, and re-validate high-stakes effects inside the tool.
- **Feeding paused history to plain `RunWithHistory`** synthesizes
  "interrupted" results for the pending calls (history repair). The
  model is told no outcome was produced — providers stay valid, but the
  conversation loses the gate. Resume with `RunWithDeferredResults`.
- **A paused `Result` persists like any run** — history JSON is
  unchanged — so pause and resume can span processes; the application
  holds the history and the resolutions between the two runs.
- **Re-runs are strict.** An approved re-run that errors or defers
  again fails the resume at the tool stage; there is no correction
  budget on re-runs. A well-behaved tool either acts or fails.
- **Tool-execution limits count re-runs, not deferrals.** A
  usage-limit `ToolCalls` bound of 1 is consumed by the approved
  re-execution, not by the pausing pass.
- **No provider involvement.** Deferral is conversation-level
  orchestration; between pause and resume no model call happens, so no
  provider ever sees an unanswered tool call.

Decisions live in `docs/adr/0018-deferred-tools.md`.
