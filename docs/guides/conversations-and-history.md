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

## API surface

- `(*Agent).RunWithHistory(ctx, runCtx, history []model.Message, prompt) (Result[Output], error)`
- `(*Agent).RunStreamWithHistory(ctx, runCtx, history, prompt, onDelta)`
- `golem.WithInstructions[Deps, Output](string)`
- `golem.WithInstructionsFunc[Deps, Output](InstructionsFunc[Deps])`

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
- Decisions live in `docs/adr/0005-message-history.md`.
