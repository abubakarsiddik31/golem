# ADR 0041: The v1 freeze

## Status

Accepted. The last pre-v1 decision; the next release is v1.0.0.

## Context

The pre-freeze series is complete. Ten items shipped in v0.9.0; the
parity series that followed — tool search, benchmarks, run boundary
events and the tracing port, sessions, the orchestration recipe,
retrieval, DeepSeek and Groq provider support, prompt templating, and
the caching ledger entry — closed the named gaps between Golem and the
frameworks it was measured against, each through the same lens: a port
where the seam is real, a recipe where the language already does it, a
preset where only configuration differs.

The v1 criteria set at the series' start stand met:

- **Documentation complete.** Thirty-one guides, one per capability,
  each with purpose, contract, runnable example, API surface, and
  gotchas; the README indexes guides and examples and does not drift
  ahead of them.
- **No known provider gaps.** Five first-class adapters (OpenAI,
  Anthropic, Gemini, Bedrock, Azure), a preset for Groq, reasoning
  capture for DeepSeek-shaped endpoints, embeddings, and token
  counting; every OpenAI-compatible endpoint reachable through one
  `BaseURL`.
- **MCP shipped.** Client over stdio and streamable HTTP, dual-era
  protocol negotiation, tools with metadata and structured results,
  progress, elicitation, resources and prompts, and an authorization
  boundary that leaves OAuth policy to the application.
- **The public surface reviewed against real usage.** The parity
  series was that review: every item landed through the API-design
  skill's sequence, every exported name has a representative caller
  test, and `scripts/apidiff.sh` held the surface additive-only
  through all of it.

What was never settled is the promise that makes the number 1 mean
something: what v1 commits to, what may still change, and how a
promise holder can tell.

## Decision

**Golem v1 follows SemVer, and below v2 the exported API is
additive-only.**

Concretely, from v1.0.0:

- **Nothing exported is removed, renamed, moved, or signature-changed
  below v2.** Packages keep their names; functions keep their
  signatures; types keep their methods. `scripts/apidiff.sh` is the
  mechanism, and it fails a change that breaks comparability.
- **Additive means additive.** New packages, new types, new methods,
  new option functions, new exported fields on existing structs, new
  event kinds, new error variants — all allowed, because existing
  programs compile against them unchanged. New struct fields keep
  struct literals comparable where callers construct them positionally
  in exported APIs; callbacks and optional collaborators stay behind
  pointers and interfaces, which is already the house style.
- **The durable contracts stay durable.** `model.Message` JSON only
  ever gains fields — that contract predates v1 and is under
  continuous fuzz. Run event kinds only ever gain members, and
  observers that ignore unknown kinds keep working. Attribute keys in
  `telemetry` and session semantics in `session` are stable names.
- **Deprecations are documentation, not removals.** A deprecated name
  keeps working, with its replacement named beside it, until v2.
- **Behavior fixes are not breaking.** A wrong error classification, a
  race, a normalization bug: fixing these is the point of v1.x, even
  when a program depended on the wrong behavior. Tests that encoded a
  bug get updated; the promise is about the API surface and the
  documented contracts, not about freezing defects.
- **Every release diffs the last v1 tag.** At release time,
  `apidiff` runs between the previous v1 tag and the release commit.
  CI's origin/main diff catches in-flight drift; the tag diff is the
  release gate.

**The post-v1 deferral list is explicit, so silence never reads as
forgetting.** Deferred to additive minors, demand-gated: stream retry
beyond the first fragment; toolset grouping; per-step tool
preparation; native provider-executed tools; MCP server mode; and the
Agent2Agent protocol — the last two being agent-*serving* surfaces,
the same category of commitment, and both blocked on users asking.
None is blocked by the shipped port set: MCP server mode can expose
the same agent the delegation tool already wraps, and an A2A adapter
would be one more implementation of the seams v1 froze.

**The non-goals survive the freeze unchanged.** No evals harness, no
graph or workflow engine, no durable execution, no CLI, UI, or gateway
products. The freeze locks the surface; it does not quietly enlarge
the mission.

## Alternatives considered

**Freeze nothing — keep shipping minors with breaking changes until
the surface feels done.** "Done" never arrives; every framework that
stayed 0.x taught its users to pin exact versions and read changelogs
like patch notes for landmines. The number 1 is the promise that
upgrading is safe, and the whole point of the parity series was to
make that promise cheap to keep.

**Freeze the surface but allow breaking changes marked as minor.**
That is SemVer's 0.x convention wearing a 1.x label. Users cannot
automate against it; the promise would be the changelog's word, and
the changelog is written by the breaker.

**A longer freeze list** — freeze behavior, performance, error
strings. Behavior fixes and error-text polish are maintenance; a
promise that forbids them converts bug reports into feature requests.
What is promised is the API surface and the documented contracts, and
the documented contracts were written to be keepable.

**Freeze later, after the deferred items land.** Each deferral was
decided on evidence already: MCP server mode and A2A have no
requesters, and the port seams they would use are the ones this ADR
freezes. Waiting would freeze the same seams later, after more
surface had accumulated to review.

## Consequences

Upgrading to v1.x is a non-event: `go get -u` and read the changelog
for new capabilities. Breaking changes accumulate on main behind the
v2 label, and the changelog's Breaking section starts empty and stays
empty until that label exists.

The cost is real and accepted: fixing a mistake in a v1 name means
living with it until v2 or adding a parallel name. That cost is the
product — it is what makes every other promise in this document
believable — and the API-design process that gated every addition to
the surface is what keeps the price low.

The frozen seams are also the extension points: `model.Model`,
`tool.Tool`, `embedding.Embedder`, `tokens.Counter`, `session.Store`,
`retrieval.Index`, `telemetry.Tracer`, `mcp.Transport`. Providers,
backends, and protocols that do not exist yet plug in behind
interfaces; v2 is not on the path to any of them.
