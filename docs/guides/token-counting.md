# Token counting

## Purpose

Ask a provider how many tokens a request would consume before sending
it — to bound one request up front and to bound history by budget
instead of message count.

## When to use

Use a counter when the cost of an oversized request is worse than the
cost of an extra HTTP call: hard per-request ceilings, narrow context
windows, or conversations that grow unboundedly. When a simple
message-count trim suffices, `golem.TrimHistory` needs no counter and
makes no calls. Applications can implement `tokens.Counter` themselves
where no provider endpoint exists — an OpenAI-compatible endpoint has
none, so Golem ships counters only for Anthropic, Gemini, and Bedrock.

## How it works

`tokens.Counter` is a one-method port: give it the would-be request —
messages plus the tools the run would advertise — and the provider
returns its input-token count. Adapters reuse each provider's request
builders, so a count prices exactly what inference would send; the
result never includes a would-be response.

Two features sit on the port:

- **Pre-send limits.** Wire a counter with `golem.WithTokenCounter` and
  set `UsageLimit.PerRequestInputTokens`. Before every model call the
  runner prices the request it is about to send; an estimate over the
  bound fails the run at the usage stage with a `UsageLimitError`
  naming the crossing — the request never reaches the provider. A
  counting failure (provider down, unsupported model) fails the run at
  the model stage; evidence up to the last completed turn is preserved
  either way. Setting the bound without a counter fails `golem.New`.
- **Budget bounding.** `golem.BudgetHistory(counter, maxTokens)` is the
  token-budget sibling of `TrimHistory`. It counts the history and,
  while over budget, drops the oldest message and counts again — under
  the same boundary rule `TrimHistory` uses, so no cut ever separates a
  tool call from its result. It returns the newest suffix that fits,
  and fails the run when the newest openable turn alone exceeds the
  budget.

Counting is one HTTP call per count: the pre-send check prices each
model turn, and `BudgetHistory` makes at most one call per dropped
message plus one. On Bedrock the count is free of charge; on Anthropic
and Gemini it bills as a normal (non-inference) request — check your
plan. The support matrix and per-provider caveats live in the
[providers guide](providers.md).

## Example

`examples/token-counting` counts a conversation with the Anthropic
counter, bounds it with `BudgetHistory`, and enforces a per-request
limit — printing instructions and exiting when `ANTHROPIC_API_KEY` is
unset. Offline tests cover the same mechanics with
`testmodel.CountFunc`.

```go
counter, err := anthropic.NewCounter(anthropic.Config{
    APIKey: os.Getenv("ANTHROPIC_API_KEY"),
    Model:  "claude-sonnet-4-5",
})
agent, err := golem.New[deps, string](client, decoder,
    golem.WithTokenCounter[deps, string](counter),
    golem.WithHistoryProcessor[deps, string](golem.BudgetHistory(counter, 8_000)),
    golem.WithUsageLimit[deps, string](golem.UsageLimit{PerRequestInputTokens: 100_000}),
)
```

## API surface

- `tokens.Counter` — the port: `CountTokens(ctx, tokens.CountInput) (int, error)`.
- `tokens.CountInput{Messages, Tools}` — the request shape a counter prices.
- `anthropic.NewCounter(anthropic.Config) (*anthropic.Counter, error)` —
  Messages API count-tokens endpoint; counts tools.
- `gemini.NewCounter(gemini.Config) (*gemini.Counter, error)` —
  countTokens endpoint; counts tool declarations.
- `bedrock.NewCounter(bedrock.Config) (*bedrock.Counter, error)` —
  CountTokens API; messages and system only, so a count with tools is a
  lower bound.
- `testmodel.CountFunc` — adapt a function to the port for tests.
- `golem.WithTokenCounter[Deps, Output](tokens.Counter)` — wire a counter.
- `golem.UsageLimit.PerRequestInputTokens` — the pre-send bound.
- `golem.BudgetHistory(tokens.Counter, maxTokens int)` — the budget processor.

## Gotchas

- A Bedrock count never includes tool definitions — the endpoint has no
  field for them — so estimates there under-price tool-heavy runs.
  AWS also rejects counting for inference-profile model IDs (the
  `us.`/`eu.`/`global.` prefixes); use the base foundation-model ID.
- Counts price the request only. Output tokens, tool-call growth
  between turns, and the run's instructions-plus-tools overhead are
  your margin: prefer a generous budget over a tight one.
- `BudgetHistory` counts the history it receives — the run's tools and
  instructions are not included, so fold their size into the budget you
  choose.
- The pre-send check runs before every model call, including correction
  rounds and retried attempts' first send; that is one extra HTTP call
  per turn, which is the price of the guarantee.
- No counter exists for OpenAI, Azure OpenAI, or OpenAI-compatible
  endpoints (they have no counting API); implement `tokens.Counter`
  with your own tokenizer there.
- The deciding design record is ADR 0023 (`docs/adr/0023-input-token-counting.md`).
