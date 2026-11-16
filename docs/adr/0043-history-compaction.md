# ADR 0043: Summarizing history compaction

Date: 2026-11-15
Status: Accepted

## Context

Golem bounds a conversation with `TrimHistory` and `BudgetHistory`,
both of which drop old messages. Dropping is free and deterministic,
but it forgets everything before the cut — exactly the context a long
session needs. Pydantic AI's compaction surface covers both ends:
zero-cost trimming and LLM summarization, with provider-native
compaction where the API offers it.

The frozen v1 surface already exposes the seam this needs: a
`HistoryProcessor` rewrites the history of one run before the request
is built, and `WithHistoryProcessor` installs it. Adding a built-in
processor is additive and leaves the runner, message contract, and
provider adapters untouched.

## Decision

`golem.SummarizeHistory(summarize SummaryFunc, keepRecent int)` returns
a `HistoryProcessor` that replaces every message older than the newest
`keepRecent` with one user-role message carrying the summary.
`SummaryFunc` is `func(ctx context.Context, messages []model.Message)
(string, error)` — the application supplies the model call, so the core
keeps no provider dependency and the summarizer can be cheaper than the
agent's model.

The cut reuses `TrimHistory`'s turn-boundary rule: advance past tool
results and assistant turns carrying tool calls, so a call and its
result are summarized together rather than split. System messages in
the summarized prefix are omitted, because the run's current
instructions replace history system messages on every request; folding
a stale prompt into user history would grant it authority it does not
have.

The summary is framed in the user voice as historical context, not new
instructions, and carries no run or conversation identity. A nil
summarizer, negative `keepRecent`, summarizer error, or empty summary
fails the run before any model request.

## Alternatives considered

- **Provider-native compaction** (OpenAI `/responses/compact`,
  Anthropic on-demand compaction): each needs a provider endpoint and a
  canonical compaction message part, so it is provider-specific surface
  the core would carry forever. Deferred until demand justifies the
  message-contract change.
- **Compact before every model request inside the runner**: closer to
  Pydantic's per-request behavior, but it would rewrite `Result.Messages`
  evidence mid-run, hide tool results the application may need, and
  require a new runner hook. The history processor runs once per run and
  keeps the canonical transcript append-only.
- **A `compaction` subpackage**: the root package already owns
  `TrimHistory` and `BudgetHistory`; splitting one helper from its
  siblings would make callers import two packages for one option.
- **A built-in summarizer model wrapper**: would couple the core to a
  prompt and a model-calling policy. A function type keeps the choice —
  and the cost — with the application.

## Consequences

- New exported names: `SummaryFunc` and `SummarizeHistory`. Both are
  additive to the v1 surface; no existing behavior changes.
- Summaries are lossy. The guide states that tool results and exact
  wording before the cut are gone, and that mid-run growth still needs
  trimming plus usage limits.
- The processor runs at run start, so a single long tool loop is not
  compacted. Per-request compaction remains a post-v1 extension if users
  need it.
- Contract tests cover the prefix sent to the summarizer, the kept
  window, boundary advancement, system-message omission, degenerate
  input, and the failure path proving no model call happens.
