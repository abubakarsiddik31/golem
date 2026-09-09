# ADR 0024: User-supplied pricing

## Status

Accepted.

## Context

`Result` carries usage and activity counts but no cost, and usage limits
bound tokens — a proxy for spend, not spend itself. Pydantic AI prices
runs (`RunUsage.cost`, `UsageLimits.cost_limit`) but sources rates from
a dedicated pricing library, `genai-prices`, whose entire reason to
exist is that providers publish no machine-readable pricing and price
data rots: it tracks historic prices, mid-lifecycle price changes, and
tiered schedules, and still makes user-supplied rates a first-class path
for unlisted models. No Go agent framework ships cost tracking; the
ecosystem leaves it to observability platforms with their own price
tables.

The combination rule is also provider-specific, and it is already
documented in the providers guide: cached input is a subset of the
input total on OpenAI-compatible APIs and Gemini but reported beside
input on Anthropic and Bedrock, and cache writes are a premium that
only the beside-input reporters expose. A single rate sheet cannot
price both shapes correctly.

## Decision

Cost enters through a one-method port in the core: `model.Price`, whose
`Cost(Usage) float64` prices one generation's recorded usage in US
dollars. Golem ships no price table — pricing data rots, and a small
core does not carry it — so rates come from the application or from the
adapter packages. Each generation adapter gains a `Price` struct with
per-million-token USD rates that encodes its own usage semantics:
`openai.Price` and `azure.Price` treat cached input as inside the input
total (billing `Input − CacheRead` at the input rate), `anthropic.Price`
and `bedrock.Price` treat cache reads and writes as beside input, and
`gemini.Price` mirrors the OpenAI inside rule. The adapter that
normalized the usage is the authority on how its fields combine; an
application with negotiated or proxied pricing implements the port
itself.

The core consumes the port in three places:

- `WithPrice(price)` wires it. `Result.Cost` is the run's cumulative
  usage priced at response time — cost is linear over the additive
  usage fields, so pricing the accumulated total equals summing
  per-turn costs. A run without a price reports zero cost.
- `PartialResult.Cost` mirrors it on failures, so a cost ledger reads
  the same field on both paths.
- `UsageLimit.Cost` bounds the run's cumulative priced cost, checked
  with the other limits after each model response — the response that
  crosses the bound fails the run at the usage stage, evidence
  preserved. The bound without a price is a construction error, not a
  silently skipped check, matching the `PerRequestInputTokens` rule.
  `UsageLimitError.Limit` and `.Actual` widen from `int` to `float64`
  so one error shape covers every bounded dimension.

## Consequences

`Cost` is a best-effort estimate for observability and runaway
protection, not a billing system: float math, provider-reported token
counts, and whatever rates the application supplies. It prices
generation only — embeddings, counting calls, and tool costs are out of
scope — and there is deliberately no pre-send cost estimate, because a
request's cost is not knowable before its response; pre-send bounding
remains `PerRequestInputTokens`' job. Rate structs will trail provider
price changes like any data snapshot, which is exactly why they are
user-visible values and not a table the core owns.
