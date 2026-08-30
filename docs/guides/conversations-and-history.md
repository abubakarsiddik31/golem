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
and shapes only gain fields, never rename or drop them. Storage stays the
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
resolved instructions always join in full.

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

## API surface

- `(*Agent).RunWithHistory(ctx, runCtx, history []model.Message, prompt) (Result[Output], error)`
- `(*Agent).RunStreamWithHistory(ctx, runCtx, history, prompt, onDelta)`
- `golem.WithInstructions[Deps, Output](string)`
- `golem.WithInstructionsFunc[Deps, Output](InstructionsFunc[Deps])`
- `golem.WithHistoryProcessor[Deps, Output](HistoryProcessor)`
- `golem.TrimHistory(maxMessages int) HistoryProcessor`

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
- The history processor runs on exactly what the caller supplies and
  once per run: nothing re-runs it, so a summarizing processor cannot
  build on its own earlier output within one run.
- `TrimHistory` returns short histories unchanged — the boundary rule
  applies only when a cut actually happens — and fails the run when a
  cut leaves nothing that can open a request, such as a history of only
  tool results.
- Decisions live in `docs/adr/0005-message-history.md`.
