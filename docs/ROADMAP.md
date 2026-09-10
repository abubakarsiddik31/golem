# Golem roadmap

Direction, not promises: items move or drop as users report what they
need. Every shipped feature arrives with its guide, a runnable example,
and contract tests, per the contributor rules in
[AGENTS.md](../AGENTS.md).

## Where we are

v0.7.6 — six of the nine pre-freeze items have landed: a
`tokens.Counter` port prices a request before it is sent and bounds
history by token budget; user-supplied `model.Price` rates report and
bound a run's dollar cost; tools return image and document parts as
evidence and can fail definitively without consuming retry budget;
`golem.NormalizeHistory` repairs and reports damaged pairings while
keeping truncated arguments verbatim; every run stamps a fresh `RunID`
on its events, result, and messages, and conversations carry a
`ConversationID` through history and storage; and the durable
message-JSON contract is under continuous fuzz, with `-race` CI and
the offline examples running on every check.

## Toward v1.0.0 — the feature series

A parity pass against Pydantic AI and a survey of the Go ecosystem
(cloudwego/eino) produced a pre-freeze series. Each item lands as its
own PR with its guide, example, and contract tests; the hardening item
rides across the series. Items move or drop on evidence, and the
series ends in the freeze, not a date.

- ~~**Token counting and budget-aware history bounding.**~~ Shipped in
  v0.7.6 (ADR 0023).

- ~~**Tool-result parts.**~~ Shipped in v0.7.6 (ADR 0025), including
  the definitive-failure semantics (`&tool.Failed{}`) it absorbed.

- ~~**Result cost.**~~ Shipped in v0.7.6 (ADR 0024).

- ~~**History repair.**~~ Shipped in v0.7.6 as `golem.NormalizeHistory`
  with the per-adapter `truncated_args` wire encoding (ADR 0026).

- ~~**Run and conversation IDs.**~~ Shipped in v0.7.6 (ADR 0027).

- **In-tool run cancellation.** A typed way for a tool to end the run
  cleanly, preserving evidence through `RunError.Partial`.

- **Untrusted-history sanitization.** A validation pass for
  client-supplied histories: foreign system prompts, dangling tool
  calls, unsafe URL schemes.

- **Tool search / deferred tool loading.** Model-driven discovery over
  large tool lists. Needs an ADR; may slip behind v1 if skills already
  cover the need.

- ~~**Test hardening.**~~ Shipped in v0.7.6: race-detector CI,
  continuous fuzzing of the message-JSON contract, the opt-in live
  smoke matrix, and CI running the offline examples.

After the series: the freeze ADR — the compatibility promise,
additive-only below v2, deprecations removed only at the next major,
checked by apidiff between the v1 tag and main on every release — and
the v1.0.0 cut. The v1 criteria stand: documentation complete, no
known provider gaps, MCP shipped, and a review of the public surface
against real usage.

## After v1 — additive minors

- **Stream retry beyond the first fragment**, only if usage demands a
  replay-safe design; today streamed turns are single-attempt by
  design.
- **Toolset grouping**, only if MCP usage demands a grouping concept.
- **Per-step tool preparation** (filter or rewrite the tool list per
  request) and **native provider-executed tools** (server-side search,
  code execution), demand-gated.
- **MCP server mode**, exposing a Golem agent as an MCP server.

## Not planned

An evals harness, a graph or workflow engine, durable execution, and
CLI, UI, or gateway products stay out per the
[foundation brief](foundation.md) non-goals; revisit when users ask.
Agent-loop middleware frameworks and YAML agent specs are rejected for
the small core: the history-processor and run-event seams remain the
extension points.
