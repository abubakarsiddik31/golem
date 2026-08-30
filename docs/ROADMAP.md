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

## Next patch — v0.7.1

- **Gemini stream terminal integrity.** A Gemini SSE stream that ends
  without a terminal finishReason chunk — a network truncation —
  currently assembles the partial text and returns it as a complete
  answer. Fail it as a transport truncation, the way the OpenAI,
  Azure, Anthropic, and Bedrock adapters fail a missing `[DONE]` or
  message-stop sentinel.
- **Evidence-preserving failures.** Every error path discards the
  partial transcript and the usage already spent; the runner and the
  agent both return zero values today. Carry partial messages and
  usage through RunError so cancelled and mid-run-failed runs keep
  their evidence, deciding how partials ride unwrapped cancellation
  errors.

## Toward v0.8.0 — run evidence and embeddings

Verified against real usage — a RAG application built on Golem. Each
item lands as its own PR and is dogfooded before the freeze.

- **Run activity counts.** The run counts model requests and tool
  executions for the usage limit and drops them. Surface them on the
  result so cost ledgers need no inference.
- **Run-scoped run events.** WithRunEvents binds one observer per
  agent; add a run option so a shared agent can route events per
  request.
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
