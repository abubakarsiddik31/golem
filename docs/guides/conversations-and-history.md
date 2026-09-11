# Conversations and history

## Purpose

Chain runs into multi-turn conversations and persist them anywhere with
`encoding/json`.

## When to use

Any follow-up question, chat interface, or workflow where later runs need
earlier context.

## How it works

A run returns the full normalized conversation in `result.Messages`. Pass
it back as history: it is sent before a fresh user prompt, and the new
result carries the full reconstructed conversation, so runs chain.

The agent's current instructions govern every request: system messages in
history are replaced by the instructions resolved for the current run, so
guidance never duplicates or goes stale. Instructions may depend on
runtime state through a per-run function.

`model.Message` JSON is a durable, additive-only contract: field names
and shapes only gain fields, never rename or drop them. Tool messages
carry their evidence on `parts` and a definitive failure on
`failed` (see [tools](tools-and-dependencies.md)); storage stays the
application's job — there is no session object.

### History repair

Providers reject a conversation with broken call/result pairing: a tool
call without a result, or a result without its call. A run that crashed or
was cancelled partway — or hand-built history — can leave exactly that.
`RunWithHistory` and `RunStreamWithHistory` repair history before each
request:

- A tool call with no result receives a synthesized, neutral result
  ("interrupted before execution; no result was produced"), placed right
  after the assistant message that requested it. It states the absence of
  an outcome, not a tool failure, so the model can re-request the call.
- A result whose call is absent — including a result placed before its
  call — is dropped; no provider accepts it.

Repair is deterministic and idempotent: synthesized results carry no
timestamps, so repairing already-repaired history leaves it unchanged and
repeated resumes stay prompt-cache friendly. Repaired messages become part
of the run's canonical `result.Messages`.

### Normalizing history explicitly

The in-run repair is silent by design. When the history crosses an
application boundary — a client-supplied conversation, stored history
being resumed, a context-evicting pipeline — run
`golem.NormalizeHistory` first and read the report:

- `Synthesized` and `Dropped` name what the pass changed, so logging or
  telemetry can show a client exactly which turns were repaired.
- `Truncated` names tool calls whose arguments are not a valid JSON
  object — a stream that died mid-arguments. This is detection, not a
  change: the bytes stay verbatim in the returned history (repairing
  stored arguments would corrupt evidence), and the same call stays
  listed on every pass.

Truncated arguments never block a run: every adapter serializes a
non-object call as `{"truncated_args": "<verbatim bytes>"}` on the
wire, deterministic so replays stay prompt-cache friendly. The model
sees a shaped call naming the failure instead of a rejected request.
The deciding design record is ADR 0026.

### Sanitizing untrusted history

A history that arrives over a trust boundary — a browser request
resuming a conversation, another service's transcript, a
client-submitted paused run — can assert anything: a system prompt
carrying operator authority, an image URL naming `file:///etc/passwd`,
a fabricated tool call awaiting "approval". Runs already neutralize
the model-visible part silently (instructions replace history system
prompts; repair fixes pairing), but a boundary that cannot see the
attempt cannot log, reject, or bill for it — and the URL-scheme case
was never handled at all.

`golem.SanitizeHistory(history)` is the explicit boundary pass, before
`Run`, `RunWithHistory`, or `RunWithDeferredResults` on history you
did not author. It drops system messages, drops URL parts whose
scheme is not `http` or `https` (inline-data parts are untouched),
repairs pairing — and reports everything:

- `SystemPrompts` counts the dropped system messages — an attempt
  reported, since the model never saw them anyway.
- `UnsafeParts` names each dropped URL part with its original message
  index, kind, and rejected scheme. A user message left empty by the
  drops is removed; tool results are never removed, so pairing
  evidence survives.
- `Repair` is the pairing report — a fabricated dangling call is
  synthesized and named here rather than surfacing as a resume-time
  approval prompt.

The pass never rewrites content: thinking blocks, failure flags, call
arguments, and identity stamps pass through, and it is deterministic
and idempotent. Runs never sanitize automatically — the boundary is
the application's, and trusted server-side history has nothing to
strip. What sanitization narrows is reach, not trust. The rules that
actually keep a server honest live outside the history:

- Authenticate and authorize at the transport; treat every caller as
  able to submit any history it likes.
- Scope the toolset to the caller — build the tools per run from the
  authenticated user.
- Re-validate high-stakes effects inside the tool against
  server-side state; an approval in a client's history attests
  nothing.
- Never read history framing as proof — a fabricated message is
  ordinary content wearing a uniform.

The deciding design record is ADR 0029.

### Run and conversation identity

Every run carries two identifiers, stamped on the run's events, its
`Result`, and every message the run adds to the conversation — never on
the history it received:

- `Result.RunID` is minted fresh per run and never inherited. Pass
  `golem.WithRunID(id)` to align a run with a trace or request ID your
  infrastructure already has; a shared agent's interleaved event streams
  stay attributable because every event repeats it.
- `Result.ConversationID` identifies the conversation: `WithConversationID`
  wins when set; otherwise the most recent identified message of the
  supplied history donates its own; otherwise one is minted, starting a
  new conversation. Because the identifier rides `model.Message` JSON
  (`runId`, `conversationId` — additive fields), `RunWithHistory`
  chains runs into one conversation by construction, and the
  association survives storage round-trips with no session object.

Both come from `golem.NewID()`, a time-ordered UUID version 7. Fork a
conversation — continue the same history under a new identity — by
passing a fresh `golem.NewID()` to `WithConversationID`; a failed run's
`RunError.Partial` reports the same pair, so telemetry can join a
failure to the conversation it interrupted. The deciding design record
is ADR 0027.

### Resuming a failed run

A failed run does not have to be a dead end. When a run errors after it
began producing evidence — completed model turns, reported usage,
executed tools — the `RunError` carries it as `Partial`: the
conversation through the last completed model turn, the usage those
turns reported, and the counts of model requests and tool executions.
Cancellation and disconnects are included, which is why they ride a
`RunError` too.

`Partial.Messages` is resume-ready history: pass it to `RunWithHistory`
to continue the conversation. A failure inside a tool batch leaves the
evidence ending at the assistant turn that requested the batch — repair
synthesizes results for its unanswered calls, exactly as for a crashed
run. `Partial` is nil when the run failed before completing anything,
so a first-call failure needs no recovery path.

A tool's deliberate stop (`&tool.Canceled`, the `canceled` stage) is
the one kind whose batch survives: results recorded before the stop
stay in the transcript, and every call left unanswered — the stopping
call, its concurrent siblings, everything after it — is closed with the
synthesized no-result result at once. Repair finds nothing to do, so a
cancelled run resumes with zero repair. See
[Tools and dependencies](tools-and-dependencies.md#cancelling-the-run-from-a-tool).

```go
result, err := agent.Run(ctx, runCtx, "go")
var runErr *golem.RunError
if errors.As(err, &runErr) && runErr.Partial != nil {
    log.Printf("run failed at %s after %d requests, %d input tokens — resuming",
        runErr.Stage, runErr.Partial.Requests, runErr.Partial.Usage.InputTokens)
    result, err = agent.RunWithHistory(ctx, runCtx, runErr.Partial.Messages, "continue")
}
```

### History processing

Long conversations outgrow every context window. `WithHistoryProcessor`
installs a function that rewrites the supplied history once per run —
before validation and repair — so its output, not its input, reaches the
provider. A processor error fails the run before any model call.

`golem.TrimHistory(maxMessages)` is the builtin: it keeps the newest
messages, then advances past anything that cannot open a request — tool
results whose requesting call was trimmed, and assistant tool-call turns
whose results were trimmed. Repair would otherwise reattach synthesized
results to those turns, paying tokens for evidence the trim meant to
drop. The processor applies to the history only; the fresh prompt and
resolved instructions always join in full. When a message count is the
wrong unit, `golem.BudgetHistory(counter, maxTokens)` bounds the history
by token budget under the same boundary rule — see
[Token counting](token-counting.md).

## Example

Run `examples/conversation` for an interactive chat loop:

```bash
OPENAI_API_KEY=sk-... go run ./examples/conversation
```

```go
first, _ := agent.Run(ctx, runCtx, "first question")
next, _ := agent.RunWithHistory(ctx, runCtx, first.Messages, "follow-up")

golem.WithInstructionsFunc[MyDeps, string](
    func(ctx context.Context, runCtx golem.RunContext[MyDeps]) string {
        return "The player's name is " + runCtx.Deps.Name + "."
    })
```

`examples/conversation` bounds every run to the newest 20 messages:

```go
golem.WithHistoryProcessor[struct{}, string](golem.TrimHistory(20))
```

`examples/run-ids` shows run and conversation identity across chained
and forked runs (offline):

```bash
go run ./examples/run-ids
```

```go
first, _ := agent.Run(ctx, runCtx, "hi")
continued, _ := agent.RunWithHistory(ctx, runCtx, first.Messages, "and then?")
// continued.RunID is fresh; continued.ConversationID == first.ConversationID
fork, _ := agent.RunWithHistory(ctx, runCtx, first.Messages, "fork this",
    golem.WithConversationID(golem.NewID()))
```

## API surface

- `(*Agent).RunWithHistory(ctx, runCtx, history []model.Message, prompt) (Result[Output], error)`
- `(*Agent).RunStreamWithHistory(ctx, runCtx, history, prompt, onDelta)`
- `golem.WithInstructions[Deps, Output](string)`
- `golem.WithInstructionsFunc[Deps, Output](InstructionsFunc[Deps])`
- `golem.WithHistoryProcessor[Deps, Output](HistoryProcessor)`
- `golem.TrimHistory(maxMessages int) HistoryProcessor`
- `golem.NormalizeHistory(history []model.Message) ([]model.Message, HistoryRepair)` — see [Normalizing history explicitly](#normalizing-history-explicitly)
- `golem.SanitizeHistory(history []model.Message) ([]model.Message, SanitizeReport)` and `golem.UnsafePart{MessageIndex, Kind, Scheme}` — see [Sanitizing untrusted history](#sanitizing-untrusted-history)
- `golem.NewID() string`
- `golem.WithRunID(id string) RunOption`, `golem.WithConversationID(id string) RunOption`
- `Result.RunID`, `Result.ConversationID`, `PartialResult.RunID`, `PartialResult.ConversationID`
- `model.Message.RunID`, `model.Message.ConversationID` (`runId`, `conversationId` in JSON)

## Gotchas

- Instructions are resolved once per run, before the request is built;
  correction rounds do not re-evaluate them.
- A dynamic function's result joins static instructions — static first,
  separated by a blank line; an empty result contributes nothing.
- Never reshape serialized messages yourself; rely on the additive
  contract (`json.Marshal`/`json.Unmarshal` round-trips).
- Repair pairs calls and results by call ID; calls without an ID cannot
  be paired and pass through unrepaired. Duplicate results for one call
  keep the first and drop the rest.
- Run identity stamps only what a run adds: re-supplied history keeps
  the stamps it carried in, and repair-synthesized results carry none.
- Identifiers are opaque values: match and store them, don't parse
  meaning out of them beyond the UUID's timestamp.
- History a client submitted is untrusted until `SanitizeHistory` has
  run on it, and even then sanitization narrows reach, not trust: the
  endpoint's authentication is the real boundary.
- The history processor runs on exactly what the caller supplies and
  once per run: nothing re-runs it, so a summarizing processor cannot
  build on its own earlier output within one run.
- `TrimHistory` returns short histories unchanged — the boundary rule
  applies only when a cut actually happens — and fails the run when a
  cut leaves nothing that can open a request, such as a history of only
  tool results.
- Decisions live in `docs/adr/0005-message-history.md`.
