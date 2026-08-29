# Golem guides

Guides are the source of truth for user-facing behavior. Each one covers
a capability end to end — purpose, contract, a runnable example, the API
surface, and gotchas — and links the ADR that decided it. The README
indexes them; it must not drift ahead of them.

New guides start from `TEMPLATE.md` in this directory (excluded from the
published site). A behavior change is only complete when its guide, its
example, and the README index agree.

## Reading order

1. [Getting started](getting-started.md) — the smallest agent and where everything lives.
2. [Providers](providers.md) — connecting OpenAI-compatible and Anthropic APIs.
3. [Tools and dependencies](tools-and-dependencies.md) — typed tools that receive run dependencies.
4. [Web fetch](web-fetch.md) — the webfetch common tool: URLs as agent-readable text.
5. [File read](file-read.md) — the fileread common tool: workspace files as agent-readable text.
6. [Command execution](command-execution.md) — the shell common tool: one command, combined output.
7. [MCP client](mcp-client.md) — bridging Model Context Protocol servers into agent tools.
8. [Agent delegation](agent-delegation.md) — one agent as another agent's tool.
9. [Tool timeouts](tool-timeouts.md) — context-aware deadlines for individual tool calls.
10. [Conversations and history](conversations-and-history.md) — multi-turn runs, durable message JSON, and history trimming.
11. [Multimodal input](multimodal-input.md) — images in prompts, per-provider mapping.
12. [Structured output](structured-output.md) — declaring the answer shape and decoding it.
13. [Self-correction](self-correction.md) — rejection budgets for output and tools.
14. [Retries](retries.md) — surviving transient model failures and falling back to another model.
15. [Streaming](streaming.md) — fragments as they arrive, same canonical result.
16. [Run events](run-events.md) — observing attempts, tool calls, and corrections as they happen.
17. [Thinking](thinking.md) — reasoning models: requesting thinking, keeping signatures, replay.
18. [Usage limits](usage-limits.md) — bounding tokens, requests, and tool calls.
19. [Testing without a provider](testing.md) — deterministic fakes and what to assert.
20. [Deferred tools](deferred-tools.md) — pausing a run for approvals or external results, and resuming.
