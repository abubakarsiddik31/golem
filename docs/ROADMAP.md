# Golem roadmap

Direction, not promises: items move or drop as users report what they
need. Every shipped feature arrives with its guide, a runnable example,
and contract tests, per the contributor rules in
[AGENTS.md](../AGENTS.md).

## Where we are

v0.7.4 — cache economics are first-class: `model.Usage` carries the
provider's cache-read, cache-write, and reasoning token breakdown
beside the totals (summed across turns like the rest), and the
adapters expose prompt caching where the provider offers control —
`anthropic.Config.CacheControl` and `bedrock.Config.CacheControl` —
while OpenAI, Azure, and Gemini cache implicitly with hits reported
the same way. A cost ledger can now read a run's activity, finish
reason, and priced token detail straight off the result.
On the v0.7.0–v0.7.3 foundations — failures keeping their evidence via
`RunError.Partial`, reasoning as first-class evidence with provider
signatures, tools that pause for human approval or external results,
local runtimes through a base URL, composition and control (agent
delegation, run events, request tuning), the common tools, the MCP
client over stdio and streamable HTTP, and streaming on every adapter —
the core execution contract is complete for single-agent applications.

## Toward v0.7.5 — embeddings

Verified against real usage — a RAG application built on Golem. The
item lands as its own PR and is dogfooded before the freeze.

- **Embeddings.** A provider-neutral Embedder port — the
  query/documents split is the task-type encoding — with adapters
  where the provider offers one (Gemini; OpenAI-compatible, which
  covers Azure and local runtimes; Anthropic has none), usage
  reporting, and a test double. Vector stores, chunking, and
  rerankers stay application concerns.

Then the freeze ADR and v1.0.0.

## After v1 — additive minors

- **Token-aware history bounding.** A count-tokens capability where
  providers expose one, and a token-budget history processor;
  TrimHistory stays message-count based.
- **Tool-result parts.** Non-text tool results — document images,
  screenshots — a durable message-contract extension with uneven
  provider support; needs an ADR.
- **Pre-send usage-limit estimation**, once token counting exists;
  today the check is post-response by design.
- **Stream retry beyond the first fragment**, only if usage demands a
  replay-safe design; today streamed turns are single-attempt by
  design.

## Toward v1 — completeness and freeze

- **Toolset grouping**, only if MCP usage demands a grouping concept.
- **v1 criteria.** Documentation complete, no known provider gaps, MCP
  shipped, and a review of the public surface against real usage — then
  the compatibility promise starts.

## Not planned

An evals harness, a graph or workflow engine, durable execution, and
CLI, UI, or gateway products stay out per the
[foundation brief](foundation.md) non-goals; revisit when users ask.
