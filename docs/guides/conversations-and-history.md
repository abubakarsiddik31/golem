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
- Decisions live in `docs/adr/0005-message-history.md`.
