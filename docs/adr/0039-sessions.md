# ADR 0039: Sessions — a storage port and the WithSession option

## Status

Accepted. Reverses one documented stance, deliberately.

## Context

Conversations in Golem are explicit: a run returns its full
conversation in `Result.Messages`, the caller persists it however they
like, and the next run passes it back as history. The guide said so in
so many words — "storage stays the application's job — there is no
session object" — and ADR 0027's identity work made messages carry
their conversation identity through any storage round-trip.

Pydantic AI's parity surface is a session: storage backends keyed by a
session ID, with the agent loading before and saving after each turn.
Golem's explicit-history flow is more transparent and always worked,
but it makes every chat application re-implement the same three lines
— load, run, save — and one class of bug: storing nothing when a run
fails partway, so a crashed chat loses its transcript precisely when a
resume would help.

The module was not willing to take a database driver for this. But
the objection to a session concept was never the concept; it was
hidden persistence and hidden state. Those are avoidable.

## Decision

A two-method port and one run option.

`package session` defines `Store`: `Load(ctx, conversationID)`,
`Save(ctx, conversationID, messages)`, value semantics — Save replaces
the conversation wholesale, Load returns what Save stored, and
implementations must snapshot because callers keep mutating their
slices. The module ships `session.Memory`, an in-process store whose
snapshot is the durable message JSON itself, so a stored conversation
shares nothing with the caller that saved it and doubles as a
round-trip check against the storage contract. Every other backend —
file, SQLite, Redis — stays application-side behind the two methods.

`golem.WithSession(store)` is a run option. A sessionful run resolves
its conversation ID exactly as always — pinned by
`WithConversationID`, else inherited from explicit history, else
minted — and then:

- **Loads** the stored conversation before the run, when and only when
  the caller passed no history. Loaded history flows through the same
  history processor and prompt-input validation as caller-supplied
  history, because it is loaded inside `prepareRun`, not around it.
- **Saves** after every run: `Result.Messages` after success,
  `RunError.Partial.Messages` after a failure, nothing when a failure
  produced no evidence. A paused run saves its transcript — a process
  that dies holding an approval prompt leaves a resumable store.

Two failure rules keep the seams honest. A load failure fails the run
before any model call, a plain wrapped error like every other
pre-run validation. A save failure does not fail the run's result: the
model did answer, and throwing the answer away because a disk hiccuped
would trade a bookkeeping error for a user-visible one. The call
returns the result **and** the save error; when the run had already
failed, the two errors are joined.

The save runs under `context.WithoutCancel`, because evidence
surviving cancellation is the run contract — a deadline-exceeded run
is exactly the run whose transcript must reach the store.

## Alternatives considered

**Keep explicit history only, document a recipe.** Honest, and it is
what the guide did. The cost is the repeated three lines and the
missing failure path: nobody's recipe saves partial evidence, and the
resume-from-store story this ADR buys is the actual parity gap.

**Construction-scoped sessions** (`golem.WithSessionStore` on the
agent) — every run of that agent persists automatically. A shared
agent serves many conversations, and the conversations that should
persist are a per-request decision in a server. Run-scoped matches
`WithRunObserver` and `WithConversationID`; a service can wrap it in
one helper if it wants the automatic flavor.

**A richer Store** — append, list, delete, TTL, watch. Every one of
those is backend policy, and the contract would grow to a small
database. Load-and-replace covers the conversation lifecycle;
everything else is a backend feature the application already owns.

**Saving only on success.** Simple, and it deletes the transcript of
exactly the runs that need resuming. Partial-evidence saves are the
point of the feature.

**Deep-copying messages in the port.** Snapshot semantics can be met
by JSON (Memory) or by copy-on-write (a backend that encodes on Save
anyway); requiring a deep copy in every implementation would just move
the same work around while forbidding the cheapest correct one.

## Consequences

A chat becomes `WithSession(store)` plus a pinned conversation ID; no
history handling. The stored transcript includes failures and pauses,
so "continue that conversation" works after a crash, and the durable
message-JSON contract is what makes any backend trivial to write.

The reversal is real and bounded: the guide's "storage stays the
application's job" now reads "the backend stays the application's
job" — the application still owns where conversations live, and
nothing persists unless a run opts in. A sessionless run touches no
store, and `WithSession(nil)` is a no-op.

Concurrency is unchanged and documented: two concurrent runs
continuing one conversation race at the application layer — the second
save overwrites the first — exactly as two callers passing the same
history by hand always have. Runs of one conversation belong to one
request path, or to a Store that serializes them.

If a future need justifies a first-party file or SQLite backend, it
lands as another implementation of this port — additive, behind the
same two methods. That is the point of deciding the seam first.
