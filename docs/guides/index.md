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
3. [Embeddings](embeddings.md) — the text-to-vector port: queries, documents, and usage.
4. [Tools and dependencies](tools-and-dependencies.md) — typed tools that receive run dependencies.
5. [Web fetch](web-fetch.md) — the webfetch common tool: URLs as agent-readable text.
6. [File read](file-read.md) — the fileread common tool: workspace files as agent-readable text.
7. [Command execution](command-execution.md) — the shell common tool: one command, combined output.
8. [Agent skills](skills.md) — the skills common tool: standard SKILL.md folders loaded on demand.
9. [MCP client](mcp-client.md) — bridging Model Context Protocol servers into agent tools.
10. [Agent delegation](agent-delegation.md) — one agent as another agent's tool.
11. [Tool timeouts](tool-timeouts.md) — context-aware deadlines for individual tool calls.
12. [Conversations and history](conversations-and-history.md) — multi-turn runs, durable message JSON, and history trimming.
13. [Multimodal input](multimodal-input.md) — images in prompts, per-provider mapping.
14. [Structured output](structured-output.md) — declaring the answer shape and decoding it.
15. [Self-correction](self-correction.md) — rejection budgets for output and tools.
16. [Retries](retries.md) — surviving transient model failures and falling back to another model.
17. [Streaming](streaming.md) — fragments as they arrive, same canonical result.
18. [Run events](run-events.md) — observing attempts, tool calls, and corrections as they happen.
19. [Thinking](thinking.md) — reasoning models: requesting thinking, keeping signatures, replay.
20. [Usage limits](usage-limits.md) — bounding tokens, requests, and tool calls.
21. [Testing without a provider](testing.md) — deterministic fakes and what to assert.
22. [Deferred tools](deferred-tools.md) — pausing a run for approvals or external results, and resuming.
