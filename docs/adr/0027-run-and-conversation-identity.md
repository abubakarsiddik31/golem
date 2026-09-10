# ADR 0027: Run and conversation identity

## Status

Accepted.

## Context

A run is invisible to correlation: its events, its result, and its
messages carry no identifier, so a shared agent — a server handling many
requests — cannot attribute an interleaved event stream to the run that
produced it without wrapping every observer in per-run closures of its
own. Chained runs are equally anonymous: `RunWithHistory` continues a
conversation, but nothing says which conversation, and after a storage
round-trip the association lives only in application-side bookkeeping.
The pre-v1 series lists run and conversation identity as the smallest of
the correlation gaps, and the freeze argues for settling it now, while
the additive surface is still cheap.

The upstream reference settled the same problem and its resolution is
instructive. Pydantic AI stamps two identifiers on every message: a
`run_id` — "the unique identifier of the agent run in which this message
originated" — resolved from an explicit argument or a fresh UUID7, and
never inherited from history; and a `conversation_id` — "the unique
identifier of the conversation this message belongs to; a conversation
spans potentially multiple agent runs that share message history" —
resolved by explicit value, else the most recent identified message of
the supplied history, else a fresh UUID7, with a sentinel that forces a
new conversation for forks. Eino ships no equivalent; correlation is
left to the caller.

Golem's constraints shape the port. There is no session object and
storage stays the application's job, so identity must survive the
round-trip through `model.Message` JSON — the durable contract — rather
than living beside it in caller-managed state. History a run receives is
never rewritten: repair (ADR 0026) only adds or removes pairing
evidence, and a run that stamped its caller's messages would mutate
stored evidence. The module depends on the standard library only, so
minting cannot import a UUID package.

## Decision

Two identifiers, one resolution rule each, stamped on everything a run
produces.

**`Result.RunID`** identifies the run: minted fresh by `golem.NewID()`
per run — never inherited from history — unless the `WithRunID` run
option supplies one. It rides every event the run emits and every
message the run adds to `Messages`, and appears on `RunError.Partial`
for failed runs, so interleaved events and failures stay attributable
under concurrency. An empty option value mints, like the default.

**`Result.ConversationID`** identifies the conversation the run
continued: the `WithConversationID` option wins when set; otherwise the
most recent message of the prepared history carrying a conversation
identifier donates its own — inheritance through history is the whole
mechanism, and it survives storage because the identifier rides the
message JSON; otherwise a fresh identifier is minted, starting a new
conversation. Forking a conversation — continuing a history under a new
identity — is the explicit option fed a fresh `golem.NewID()`; the
history cannot override an explicit value, and no string sentinel
exists.

**Stamping marks what a run added, nothing else.** The fresh user
prompt (and a resume's resolutions and optional prompt) are stamped in
the request builder; the runner stamps every assistant turn and tool
result it appends, and wraps its observer so every event carries both
identifiers. Supplied history passes through with the identity it
already had — a re-supplied turn keeps its originating run's stamps,
which is precisely what the next inheritance scan reads. Repair-synthesized
results keep standing in for lost evidence and carry no run identity.
Adapters never send either field to the provider: they are identity
metadata, not content, and the durable message JSON gains them as two
additive `omitempty` fields (`runId`, `conversationId`).

**`golem.NewID()`** mints a UUID version 7 (RFC 9562) — a millisecond
timestamp plus crypto/rand bytes — so identifiers sort by creation time
and the module stays standard-library only. It is exported because the
two legitimate uses are the application's: pinning a run to a trace ID
it already has, and minting a fork's conversation identifier.

## Alternatives considered

- **Identifiers on Result and events only.** Smaller surface, but
  inheritance then requires the caller to thread the previous result's
  identifier through storage themselves — a session object by another
  name. The message JSON is already the durable contract; identity
  belongs there.
- **Upstream's `"new"` sentinel for forks.** A string that means "not
  an identifier" is magic Golem avoids; an explicit fresh identifier
  says the same thing with no special value to document or mistype.
- **Rejecting a supplied run ID already present in history** (upstream
  errors). It catches resume-with-stale-ID mistakes but forbids
  legitimate replay, and a run rewriting nothing means the overlap is
  observable, not harmful. Documented instead of enforced.
- **Importing a UUID package for the mint.** Twenty lines of
  well-specified formatting against the module's first runtime
  dependency; the module stays dependency-free.

## Consequences

Everything gained is additive: two `omitempty` fields on the durable
message JSON, two fields each on `Result`, `PartialResult`, and events,
two run options, one minting function. No signature changes, no
breaking surface. Events and results now correlate without wrapper
closures; chained runs, deferred resume, and stored-and-restored
conversations share a conversation identifier by construction. The
identifier values are opaque — callers match and store them but cannot
parse meaning out of them beyond the UUID's timestamp. Tracing
backends that want `gen_ai.conversation.id` (upstream emits it for
OpenTelemetry) can read the same value off any message; Golem emits no
telemetry of its own.
