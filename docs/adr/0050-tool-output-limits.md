# ADR 0050: Tool output truncation limits

Date: 2026-11-16
Status: Accepted

## Context

A tool result becomes conversation evidence: it is re-sent on every later
model request. A single oversized read, log, search dump, or MCP response
therefore keeps costing tokens for the rest of the run and can crowd out
the context the model needs. Pydantic AI Harness addresses this with
truncate, spill, and summarize modes.

Golem has no workspace or spill store and no generic read-back tool, so a
lossless spill mode would need new storage and a new agent-facing reader.
Summarization needs a model call inside the tool boundary. The smallest
useful first step is a deterministic truncation policy applied once, when
the result is produced, before it enters history.

## Decision

`WithToolOutputLimit(maxBytes)` sets an agent-wide cap on tool result
text. A tool can override it with the additive `tool.Tool.MaxOutputBytes`
field; zero inherits the agent setting, and the zero agent setting
disables the cap. Negative values fail `New` or `tool.New`.

The runner applies the cap after tool guards and before the tool message
is appended, so the reduced text is what the model sees, what persists in
`Result.Messages`, and what later requests re-send. The reduction keeps
the head and tail of the text and inserts a marker naming the original
size; text never exceeds the cap and is cut on UTF-8 boundaries. Result
parts are untouched, because the cap is about text volume, not evidence.

The cap is bytes, not tokens: deterministic, dependency-free, and
conservative. A caller that needs token accounting can still use
`WithUsageLimit`; a caller that needs lossless recovery or an LLM summary
can implement it inside the tool.

## Alternatives considered

- **Spill to a store with a read-back tool** (Pydantic's default): needs a
  workspace abstraction and a new tool, and changes where evidence lives.
  Deferred until applications ask for it.
- **Summarize with a model**: one extra provider call per oversized
  result, inside the tool loop, with cost and failure policy. A tool
  author can already do this in `Exec`.
- **Truncate per request**: recomputes the reduction on every turn and
  makes the transcript disagree with what was sent. The reduction is
  applied once, at production.
- **Wrap every tool at registration**: moves policy into each wrapper,
  cannot see the agent default, and hides the limit from the runner.
- **Head-only or tail-only**: loses either the header or the error at the
  end. Head and tail is the useful default for logs and structured text.

## Consequences

- New exported names: `WithToolOutputLimit` and `Tool.MaxOutputBytes`;
  both additive to the v1 surface.
- `runner.ToolConfig` gains one field. The runner helper is deterministic
  and UTF-8 safe; no goroutines or provider calls.
- Tool end events carry the reduced result, matching what the model and
  history see. The full payload is not retained; tool authors that need
  it must keep it themselves.
- Spill, summarization, and per-request bands remain post-v1 options.
- Tests cover the cap, the per-tool override, disabled behavior, parts
  preservation, persisted evidence, UTF-8 boundaries, and negative
  configuration.
