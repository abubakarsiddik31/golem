# Golem roadmap

Direction, not promises: items move or drop as users report what they
need. Every shipped feature arrives with its guide, a runnable example,
and contract tests, per the contributor rules in
[AGENTS.md](../AGENTS.md).

## Where we are

v0.7.2 — the standard skill pack: the `skills` common tool discovers
Agent Skills (`<name>/SKILL.md` folders per the agentskills.io open
standard) from application-configured directories, catalogs each
skill's name and description in the tool description, and loads the
full instructions — plus base directory and supporting files — when the
model asks for one, so procedural knowledge ships as portable folders.
Run events are run-scopable: `WithRunObserver` routes one request's
events through a shared agent, composing with the agent-level observer.
On the v0.7.0/v0.7.1 foundations — failures keeping their evidence via
`RunError.Partial`, reasoning as first-class evidence with provider
signatures, tools that pause for human approval or external results,
local runtimes through a base URL, composition and control (agent
delegation, run events, request tuning), the common tools, the MCP
client over stdio and streamable HTTP, and streaming on every adapter —
the core execution contract is complete for single-agent applications.

## Toward v0.8.0 — run evidence and embeddings

Verified against real usage — a RAG application built on Golem. Each
item lands as its own PR and is dogfooded before the freeze.

- **Run activity counts.** The run counts model requests and tool
  executions for the usage limit and drops them on success. Surface
  them on the result so cost ledgers need no inference.
- **Embeddings.** A provider-neutral Embedder port — the
  query/documents split is the task-type encoding — with adapters
  where the provider offers one (Gemini; OpenAI-compatible, which
  covers Azure and local runtimes; Anthropic has none), usage
  reporting, and a test double. Vector stores, chunking, and
  rerankers stay application concerns.

Then the freeze ADR and v1.0.0.

## After v1 — additive minors

- **Finish-reason visibility.** model.Response carries the provider's
  terminal cause (stop, length cap, safety) — today no adapter
  surfaces it, so truncation is invisible outside Gemini streams.
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
