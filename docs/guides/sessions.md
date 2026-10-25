# Sessions

## Purpose

Persist conversations between runs through a two-method port:
`WithSession` loads the stored history before a run and saves the
conversation after it — partial evidence included when a run fails.

## When to use

Any conversation that should outlive the process: chat, follow-up
workflows, resume-after-crash. When history is already in hand — a
caller passing `Result.Messages` between two calls in the same
process — plain `RunWithHistory` is simpler and needs no store. The
backend stays application code: the module ships an in-memory store,
and file or database backends implement the same interface.

## How it works

`golem.WithSession(store)` is a run option. A sessionful run resolves
its conversation ID the usual way — pinned with
`WithConversationID`, inherited from explicit history, or minted —
and then, when the caller passed no history, loads the conversation
stored under it before the run. Loaded history flows through the same
history processor and validation as history the caller passed. After
every run, the full conversation is saved back: `Result.Messages`
after success, `RunError.Partial.Messages` after a failure, nothing
when a failure produced no evidence. A paused run saves its
transcript, so a process that dies holding an approval prompt leaves
a resumable store.

A load failure fails the run before any model call. A save failure
returns the run's result **and** the save error — the model did
answer; only the persistence failed — and when the run had already
failed, both errors arrive joined.

```go
store := session.NewMemory() // or your own session.Store

first, err := agent.Run(ctx, runCtx, "who plays white?",
    golem.WithSession(store), golem.WithConversationID("game-1"))

// No history to pass: the store supplies the conversation.
second, err := agent.Run(ctx, runCtx, "what was the opening?",
    golem.WithSession(store), golem.WithConversationID("game-1"))
```

Without a pinned ID the run mints one and reports it on
`Result.ConversationID`, so a client can start a conversation server-side
and continue it later by value.

## Example

Run `examples/session` — offline: two runs chain through the in-memory
store with no history passed by hand, then a deliberately failing run
shows the resumable transcript surviving in the store.

## API surface

- `session.Store` — `Load(ctx, conversationID string) ([]model.Message, error)`, `Save(ctx, conversationID string, messages []model.Message) error`
- `session.NewMemory() *Memory` — the offline in-process store; snapshots through the durable message JSON
- `golem.WithSession(store session.Store) RunOption`

## Gotchas

- Pin `WithConversationID`, or read `Result.ConversationID` after the
  first run. A sessionful run with neither mints a fresh ID, finds
  nothing to load, and starts a new conversation in the store every
  time.
- A run given history explicitly — `RunWithHistory`,
  `RunWithDeferredResults` — bypasses loading: the caller's history is
  the conversation. It is still saved back, under the history's
  inherited identity.
- Two concurrent runs continuing one conversation race no matter the
  backend: the second save overwrites the first. Runs of one
  conversation belong to one request path, or to a Store that
  serializes them.
- `Save` sees the conversation with tool evidence, thinking blocks,
  and identity stamps verbatim — store it somewhere your threat model
  accepts. `Memory` keeps everything in the process and persists
  nothing.
- The save deliberately ignores the run context's cancellation — the
  transcript of a deadline-exceeded run is exactly the one worth
  keeping. A store that must be stoppable wraps its own operations in
  its own context.
- `WithSession(nil)` is a no-op; a sessionless run never touches a
  store.
- The reversal of "storage stays the application's job" — and the
  failure-path rules — are ADR 0039.
