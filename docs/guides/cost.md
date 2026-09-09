# Cost

## Purpose

Turn provider-reported token usage into dollars: report what a run
spent on `Result.Cost`, and optionally bound that spend with a cost
usage limit.

## When to use

Wire a price when usage counts alone don't answer the question a
budget-holder asks — dashboards that show dollars per run, runaway
protection in dollar terms, or comparisons across models where the same
token count means different money. Skip it when token bounds suffice:
without a price wired, runs are unpriced and behave exactly as before.
Golem ships no price table — pricing data rots — so the rates are
always yours, taken from the provider's price list or from your own
negotiated terms.

## How it works

`model.Price` is a one-method port: `Cost(usage) float64` prices one
generation's recorded usage in US dollars. Two layers implement it:

- **Adapter prices.** Each generation adapter's `Price` struct takes
  per-million-token USD rates and encodes how *that provider's* usage
  fields combine — the combination rule is provider-specific, so the
  adapter that normalized the usage is the authority on it. On
  OpenAI-compatible APIs and Gemini, cached input is a subset of the
  input total, so `openai.Price`, `azure.Price`, and `gemini.Price`
  bill `Input − CacheRead` at the input rate and the cache reads at
  their own rate; on Anthropic and Bedrock, cache reads and writes are
  reported beside input, so `anthropic.Price` and `bedrock.Price` bill
  each field in full at its own rate. Reasoning tokens are part of
  billed output everywhere and take no separate rate.
- **Your own price.** An application with proxied, negotiated, or
  otherwise custom pricing implements the port directly — it is one
  method over `model.Usage`.

`golem.WithPrice` wires the price. The runner prices the run's
cumulative usage after each model response — cost is linear over the
additive usage fields, so pricing the total equals summing per-turn
costs — and `Result.Cost` reports it. A run without a price reports
zero cost. `UsageLimit.Cost` turns the same computation into a bound:
after each model response the priced total is checked with the other
usage limits, and the response that crosses the bound fails the run at
the usage stage with `UsageLimitError` (Kind `"cost"`) — evidence up to
the crossing turn, its cost included, preserved on `RunError.Partial`.
Setting a cost bound without a price fails `golem.New`.

## Example

`examples/cost` prices a scripted two-turn run with `anthropic.Price`
and shows the cost limit stopping an over-budget run — no network, no
credentials, since the pricing seam is provider-agnostic.

```go
price := anthropic.Price{
    InputPerMTok:      3,
    OutputPerMTok:     15,
    CacheReadPerMTok:  0.30,
    CacheWritePerMTok: 3.75,
}
agent, err := golem.New[deps, string](client, decoder,
    golem.WithPrice[deps, string](price),
    golem.WithUsageLimit[deps, string](golem.UsageLimit{Cost: 0.50}),
)
// result.Cost reports the run's priced total; a run that crosses the
// bound fails at the usage stage with RunError.Partial.Cost preserved.
```

## API surface

- `model.Price` — the port: `Cost(model.Usage) float64`, US dollars.
- `openai.Price`, `azure.Price`, `gemini.Price` — per-million rates;
  cached input billed inside the input total.
- `anthropic.Price`, `bedrock.Price` — per-million rates; cache reads
  and writes billed beside the input total.
- `golem.WithPrice[Deps, Output](model.Price)` — wire the price.
- `Result.Cost`, `PartialResult.Cost` — the run's priced total.
- `golem.UsageLimit.Cost` — the post-response cost bound.
- `golem.UsageLimitError` with Kind `"cost"` — the crossing report.

## Gotchas

- `Cost` is a best-effort estimate, not a billing figure: float math
  over provider-reported tokens at rates you supplied. Pair a cost
  limit with your provider's own spend controls if the ceiling must
  actually hold.
- Rates are a snapshot you own. Providers change prices and release
  models at rates no in-framework table could track; re-check the price
  list when you change models.
- Cost prices generation only — embeddings, token-counting calls, and
  tool execution costs are out of scope.
- There is no pre-send cost estimate: a request's cost is not knowable
  before its response. To stop an oversized request *before* it is
  sent, bound it with `UsageLimit.PerRequestInputTokens` (see [token
  counting](token-counting.md)).
- With no cache rate set, the inside-input prices (`openai.Price`,
  `azure.Price`, `gemini.Price`) bill cached input at the input rate
  rather than letting it look free; the beside-input prices
  (`anthropic.Price`, `bedrock.Price`) bill the input total in full by
  construction. Both err high, never free.
- A delegated sub-agent's spend stays in the sub-agent's own
  `Result.Cost`, like its usage.
- The deciding design record is ADR 0024
  (`docs/adr/0024-user-supplied-pricing.md`).
