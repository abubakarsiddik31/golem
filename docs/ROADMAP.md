# Golem roadmap

Direction, not promises: items move or drop as users report what they
need. Every shipped feature arrives with its guide, a runnable example,
and contract tests, per the contributor rules in
[AGENTS.md](../AGENTS.md).

## Where we are

v0.3.1 — the core execution contract is complete for single-agent
applications: typed agents, evidence-preserving runs, self-correction,
retries with fallback models, streaming, structured output in both
schema and tool modes, tool deadlines, choice, and ordered parallelism,
multimodal image input, bounded history, usage bounds across tokens,
requests, and tool calls, deterministic test models, and five provider
adapters.

## v0.4.0 — composition and control

Compose agents, observe runs as they happen, and control generation.

| Item | Size | Notes |
| --- | --- | --- |
| Agent as a tool | M | Expose one agent's typed run as a `tool.Tool` for another agent — the Go-native route to delegation and handoffs. ADR first: the extension point crosses packages. |
| Run event stream | M | Opt-in typed lifecycle events (model call, tool start and end, usage) alongside `RunStream`. ADR first: the event contract will be hard to change later. |
| Request tuning | S | `Temperature`, `MaxTokens`, and `TopP` on provider configs, mapped per adapter. |
| Web fetch tool | S | A stdlib-only HTTP fetch tool with text extraction — the first common tool, testable offline. |
| File read tool | S | A read-only file tool confined to a configured root directory — the second common tool under ADR 0015. |
| Command tool | S | A stdlib-only command execution tool with output caps — the third common tool; opt-in and loudly documented. |

## v0.5.0 — ecosystem interop

Connect Golem to the tool ecosystem.

- **MCP client (L).** Stdio and streamable-HTTP transports speaking
  JSON-RPC with the standard library only, discovering a server's tools
  and bridging them into `tool.Tool` declarations. The largest single
  item in this roadmap; it starts with an ADR fixing the package
  boundary (a new `mcp` package, the core staying dependency-free) and
  the tool-bridging contract.

## v0.6.0 and toward v1 — completeness and freeze

- **Bedrock streaming (M).** The Converse adapter gains AWS binary
  event-stream framing; the last known provider gap.
- **Toolset grouping**, only if MCP work demands a grouping concept.
- **v1 criteria.** Documentation complete, no known provider gaps, MCP
  shipped, and a review of the public surface against real usage — then
  the compatibility promise starts.

## Not planned

Embeddings, an evals harness, a graph or workflow engine, durable
execution, and CLI, UI, or gateway products stay out per the
[foundation brief](foundation.md) non-goals; revisit when users ask.
