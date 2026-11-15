# Compaction

## Purpose

Keep a long conversation inside a model's context window by trimming
old turns, bounding them by token budget, or replacing them with a
summary.

## When to use

Any conversation that can grow past the model's window: a chat that
runs for many turns, a stored session resumed indefinitely, or an agent
replaying a long transcript. For a single short run, compaction adds
nothing; for a hard ceiling on the tokens a single request may carry,
use the pre-send limit in [Token counting](token-counting.md) instead.

Compaction is the cheaper alternative to raising the window: it keeps
the newest turns verbatim and spends a bounded amount on the rest.

## How it works

Three built-in `HistoryProcessor`s cover the common policies. A
processor runs once per run, before part validation and pairing repair,
on the history the caller supplied — including history loaded by
`WithSession` — and its output is what the run sends.

- `TrimHistory(n)` drops everything older than the newest `n`
  messages. It is free and deterministic.
- `BudgetHistory(counter, maxTokens)` drops the oldest message until
  the counter reports the remaining history fits the budget. It costs
  one count per dropped message.
- `SummarizeHistory(summarize, keepRecent)` replaces everything older
  than the newest `keepRecent` messages with one user-role message
  carrying the summary. It costs one summarizer call, and the summary
  keeps context that trimming would lose.

Every cut advances past messages that cannot open a request — a tool
result whose call was trimmed, or an assistant turn carrying tool calls
whose results were trimmed — so a call and its result stay together.
`SummarizeHistory` summarizes them as a pair and omits system messages
from the summarized prefix: the run's current instructions govern every
request, so restating a stale system prompt as user history would give
it authority it does not have.

The summary is model-written text in the user voice, framed as
historical context rather than new instructions. It carries no run or
conversation identity; it stands in for messages the conversation
already had. A summarizer error, or an empty summary, fails the run
before any model request.

## Example

[`examples/compaction`](../../examples/compaction/main.go) runs offline
against a scripted model and a deterministic summarizer, then prints
the request the agent sent:

```go
agent, err := golem.New[struct{}, string](client, decoder,
	golem.WithHistoryProcessor[struct{}, string](
		golem.SummarizeHistory(summarize, 4),
	),
)
result, err := agent.RunWithHistory(ctx, golem.RunContext[struct{}]{}, history, "What next?")
```

## API surface

- `golem.HistoryProcessor` — rewrites a run's history before the request is built.
- `golem.WithHistoryProcessor[Deps, Output](processor HistoryProcessor)` — installs one processor per agent.
- `golem.TrimHistory(maxMessages int) HistoryProcessor` — keep the newest messages.
- `golem.BudgetHistory(counter tokens.Counter, maxTokens int) HistoryProcessor` — keep the newest turns that fit a token budget.
- `golem.SummarizeHistory(summarize SummaryFunc, keepRecent int) HistoryProcessor` — replace older messages with a summary.
- `golem.SummaryFunc` — `func(ctx context.Context, messages []model.Message) (string, error)`.

## Gotchas

- Compaction runs once per run, at the run's start; it does not shrink a
  conversation that grows inside one long tool loop. Use a token-budgeted
  history plus the usage limits for that case.
- A summary is lossy: tool results and exact wording before the cut are
  gone. Keep enough recent messages that the model can still act.
- The summarizer is application code. Use a cheaper model when one is
  available, honor cancellation, and keep the prompt free of secrets you
  would not send to that model.
- `TrimHistory` and `SummarizeHistory` fail the run when no message can
  open a request; `BudgetHistory` fails when the newest openable turn
  alone exceeds the budget.
- The deciding design record is [ADR 0043](../adr/0043-history-compaction.md).
