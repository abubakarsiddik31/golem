# Golem roadmap

Direction, not promises: items move or drop as users report what they
need. Every shipped feature arrives with its guide, a runnable example,
and contract tests, per the contributor rules in
[AGENTS.md](../AGENTS.md).

## Where we are

v0.7.0 — reasoning is first-class evidence: thinking blocks with
provider signatures are captured, validated, and replayed across
turns on every adapter. Tools can pause a run for human approval or
external results, resuming in-process or across processes. Local
runtimes — Ollama and LM Studio — serve the same adapter through a
base URL. Composition and control are in (agent delegation, run
events, request tuning), the common tool trio ships (web fetch, file
read, command execution), the MCP client bridges server tools over
stdio and streamable HTTP, and every adapter streams — Bedrock
included, over its AWS binary event-stream framing. The core
execution contract underneath is unchanged and complete for
single-agent applications.

## Toward v1 — completeness and freeze

- **Toolset grouping**, only if MCP usage demands a grouping concept.
- **v1 criteria.** Documentation complete, no known provider gaps, MCP
  shipped, and a review of the public surface against real usage — then
  the compatibility promise starts.

## Not planned

Embeddings, an evals harness, a graph or workflow engine, durable
execution, and CLI, UI, or gateway products stay out per the
[foundation brief](foundation.md) non-goals; revisit when users ask.
