# Prompt context

## Purpose

Add application-derived, user-role context to every run — a memory
snapshot, retrieved passages, a tenant-specific reminder — without
pretending the text is system instructions or making the caller rebuild
its history by hand.

## When to use

When a run needs context that depends on typed dependencies and should
read as data the model can weigh. Trusted guidance belongs in
instructions; conversation history belongs in history. Use prompt
context for the middle: text the application supplies for this run, in
the user's voice.

## How it works

`WithPromptContext` installs `PromptContextFunc[Deps]`, which returns
`[]model.Message` for one run. It is evaluated once per run, after the
history processor has run and before input guards and the request are
built. The messages are inserted after history and before the new
prompt, so the request is:

```
instructions (system) | repaired history | prompt context | new prompt
```

Every message must have `RoleUser`, non-empty content or parts, and no
tool calls, tool-result fields, or thinking. Parts are validated with
`model.Part.Validate`. The run stamps its identity on each context
message, and the messages stay in `Result.Messages` as evidence. An
error from the function, or an invalid message, fails the run before any
model call.

The function runs once, even when the run makes several model turns; it
is not re-evaluated after tool results or correction rounds. Deferred
resumes evaluate it too, so a resumed run can include fresh context.
Returning no messages contributes nothing.

## Example

`examples/memory` uses prompt context
to recall a memory notebook before answering:

```go
agent, err := golem.New[user, string](client, decoder,
	golem.WithPromptContext[user, string](func(ctx context.Context, runCtx golem.RunContext[user]) ([]model.Message, error) {
		message, ok, err := memory.Snapshot(ctx, cfg, runCtx.Deps)
		if err != nil || !ok {
			return nil, err
		}
		return []model.Message{message}, nil
	}),
)
```

## API surface

- `golem.PromptContextFunc[Deps]` — `func(ctx, RunContext[Deps]) ([]model.Message, error)`
- `golem.WithPromptContext[Deps, Output](fn PromptContextFunc[Deps])`
- `model.Message{Role: model.RoleUser, Content, Parts}` — the accepted shape

## Gotchas

- Context is user-role data, not a system prompt. Put durable guidance
  and policy in `WithInstructions`/`WithInstructionsFunc`.
- The function runs once per run. If it returns the same snapshot every
  run, the conversation grows a snapshot per run; decide whether that is
  acceptable or return context only when it changed.
- Input guards screen the prompt string, not these messages. Apply your
  own validation or `guard.ToolGuard`s to untrusted context.
- Context messages are part of the result transcript and reach UI
  clients that render history. Do not put secrets there that the user of
  the UI should not see.
- A context error is returned before any model call, not wrapped in a
  `RunError` stage; treat it like a history-processor failure.
- The deciding design record is `docs/adr/0049-prompt-context.md`.
