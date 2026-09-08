# Golem roadmap

Direction, not promises: items move or drop as users report what they
need. Every shipped feature arrives with its guide, a runnable example,
and contract tests, per the contributor rules in
[AGENTS.md](../AGENTS.md).

## Where we are

v0.7.5 — inputs widen and retrieval lands: messages take documents,
audio, and video alongside images, with each adapter mapping the kinds
its provider accepts and rejecting unsupported combinations before any
request, and a provider-neutral `embedding.Embedder` port embeds
queries and corpora with adapters where the provider offers one. Vector
stores, chunking, and rerankers stay application concerns.

On the v0.7.0–v0.7.4 foundations — cache economics on `model.Usage`,
failures keeping their evidence via `RunError.Partial`, reasoning as
first-class evidence with provider signatures, tools that pause for
human approval or external results, local runtimes through a base URL,
composition and control (agent delegation, run events, request tuning),
the common tools, the MCP client over stdio and streamable HTTP, and
streaming on every adapter — the core execution contract is complete
for single-agent applications.

## Toward v1.0.0 — the feature series

A parity pass against Pydantic AI and a survey of the Go ecosystem
(cloudwego/eino) produced a pre-freeze series. Each item lands as its
own PR with its guide, example, and contract tests; the hardening item
rides across the series. Items move or drop on evidence, and the
series ends in the freeze, not a date.

- **Token counting and budget-aware history bounding.** A count-tokens
  port where providers expose one, a built-in token-budget history
  processor beside message-count `TrimHistory`, and pre-send
  usage-limit estimation on the same port. First item; replaces the
  three former After-v1 entries it absorbs.

- **Tool-result parts.** Non-text tool results — documents, images —
  as a durable message-contract extension with per-provider placement;
  needs an ADR, which also settles definitive tool failures that reach
  the model without consuming retry budget.

- **Result cost.** User-supplied `model.Price` feeds `Result.Cost` and
  an optional cost usage limit. No built-in price table: pricing data
  rots, and a small core does not ship one.

- **History repair.** An explicit normalization pass for client- or
  crash-truncated histories — synthesize results for orphaned tool
  calls, repair truncated arguments — opt-in, never silent.

- **Run and conversation IDs.** A mintable run ID stamped on events
  and results, and a conversation ID carried across `RunWithHistory`
  for correlation.

- **In-tool run cancellation.** A typed way for a tool to end the run
  cleanly, preserving evidence through `RunError.Partial`.

- **Untrusted-history sanitization.** A validation pass for
  client-supplied histories: foreign system prompts, dangling tool
  calls, unsafe URL schemes.

- **Tool search / deferred tool loading.** Model-driven discovery over
  large tool lists. Needs an ADR; may slip behind v1 if skills already
  cover the need.

- **Test hardening.** Race-detector CI, fuzzing the pinned
  message-JSON contract, an opt-in live smoke matrix across all five
  adapters, and CI running the offline examples rather than only
  building them.

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
