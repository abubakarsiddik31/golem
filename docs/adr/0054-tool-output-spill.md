# ADR 0054: Tool output spill and summarization

Date: 2026-11-16
Status: Accepted

## Context

ADR 0050 added deterministic truncation for oversized tool results. It
bounds context at the cost of losing the middle of the payload. A large
log, search dump, or document often needs all of its content, but not
all at once. Pydantic AI Harness solves this with spill-to-storage plus
a `read_tool_result` tool, and with LLM summarization bands.

Golem has no workspace abstraction, but the pieces are small: a
two-method store, a bounded read tool, and one hook at the same result
production point truncation already uses.

## Decision

Two mutually exclusive agent options extend the output policy:

- `WithToolOutputSpill(store, threshold)` stores result text at or above
  the threshold through `tool.SpillStore` and replaces it with a marker
  naming the handle, total size, and a UTF-8-safe preview. The agent
  registers `read_tool_result`, which reads a byte slice with
  `offset`/`limit`, clamped by `tool.MaxSpillReadLimit`. The read tool is
  exempt from reduction, so reading cannot spill again. `tool.MemorySpill`
  is the offline store; durable stores stay application-side.
- `WithToolOutputSummary(fn, threshold)` calls an application
  `ToolOutputSummaryFunc` synchronously with the tool name and text and
  uses the returned text. A summary error or empty result falls back to
  the truncation limit.

Policy order at result production is spill, then summary, then
truncation. Spill takes precedence when its write succeeds; a failed
write falls back to `WithToolOutputLimit`. Result parts pass through.
Thresholds must be at least 1; the options require a store/function and
cannot be combined. `read_tool_result` is reserved while spill is on.

## Alternatives considered

- **One option with banded actions** (Pydantic's bands): more expressive,
  but needs an action model, per-tool bands, and fallback chains. Two
  explicit options cover the common choices without a policy DSL.
- **A tool wrapper**: cannot register the read-back tool or see the
  agent-level fallback limit.
- **Spill as the default**: changes behavior and adds a tool to every
  agent. Truncation remains the zero-config default.
- **Summarize by default with the run's model**: hidden cost and a second
  model call inside the tool loop. The application supplies the function
  and its model choice.
- **Persist spills in the session store**: conversations and spilled
  payloads have different lifecycles; the store port keeps them separate.

## Consequences

- New exported names: `WithToolOutputSpill`, `WithToolOutputSummary`,
  `ToolOutputSummaryFunc`, `SpillReadToolName`, `tool.SpillStore`,
  `tool.MemorySpill`, and related constants; all additive.
- `runner.ToolConfig` gains spill and summary hooks. The policy is
  deterministic except for the application store/summarizer.
- Spill markers are not subject to `WithToolOutputLimit`; the marker is
  small and must keep the handle readable.
- Summaries are synchronous and may cost a model call per oversized
  result. Errors are silent fallbacks, as with truncation.
- Tests cover full restoration through the read tool, offsets and limits,
  store failure fallback, summary and summary-failure fallback,
  configuration conflicts, reserved-name collision, and the memory store.
