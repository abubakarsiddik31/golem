# ADR 0026: Explicit history normalization

## Status

Accepted.

## Context

A run's history is durable application data, and real histories arrive
damaged: a crashed or cancelled run leaves tool calls without results, a
context-evicting pipeline can leave results without calls, and a model
whose stream died mid-arguments leaves a tool call whose JSON is cut
off. Providers reject all three shapes on replay. Golem already
self-heals pairing in the run path — the request builder synthesizes a
neutral interrupted result for every unanswered call and drops orphaned
results, silently, as documented on `RunWithHistory` — but an
application that accepts histories from clients, resumes stored
conversations, or prunes context has no way to see what state its
history was in, and truncated arguments have no repair at all: the
adapters either fail to encode the request or send arguments the
provider rejects. The roadmap's pre-v1.0 series calls for an explicit,
opt-in normalization pass that is never silent.

Upstream validates the split. Pydantic AI repairs pairing before every
request, synthesizing an interrupted return whose metadata marks it as
synthesized, dropping orphaned results, and serializing truncated
arguments as `{"INVALID_JSON": "<raw>"}` so providers that require an
object still accept the call — while the history keeps the arguments
verbatim. Eino ships the same idea as a standalone middleware. The
pattern in both is: pairing repairs add or remove evidence only;
argument bytes are never rewritten in stored history.

## Decision

Two surfaces, one core pass:

- **`golem.NormalizeHistory(history) ([]model.Message, HistoryRepair)`**
  is the explicit, opt-in pass for application boundaries — accepting a
  client-supplied conversation, loading stored history, resuming after
  a crash. It runs the same deterministic pairing pass the request
  builder uses and reports everything it did: `Synthesized` names the
  call IDs that received the interrupted result, `Dropped` names the
  orphaned results removed, and `Truncated` names the calls whose
  arguments are not a valid JSON object. The pass is idempotent and
  never rewrites message content.
- **Truncated arguments become sendable at request build, per adapter.**
  A tool call whose arguments are not a valid JSON object — cut off
  mid-stream, or a scalar or array where the providers require an
  object — is serialized as `{"truncated_args": "<verbatim raw>"}` on
  the wire, in every adapter. The history keeps the raw bytes, so the
  same evidence serializes the same way on every replay; the wrapper is
  deterministic, so prompt caches stay valid. This rides the existing
  normalization helpers, and the silent in-run pairing repair is
  unchanged.

## Consequences

Normalization is visible where the application needs it and invisible
where it doesn't: `NormalizeHistory` costs one pass and a report, and
runs keep their documented self-healing so a damaged history never
blocks a resume. The `truncated_args` wrapper means a model sees a
shaped-but-unparseable call as an object naming the failure instead of
a rejected request; it is attribution, not proof — the model can guess,
but the raw bytes remain the evidence. Detection reports only;
rewriting stored arguments is deliberately out of scope, because the
durable JSON contract does not gain repair-specific shapes.
