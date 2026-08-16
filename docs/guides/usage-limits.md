# Usage limits

## Purpose

Bound what one run can consume: input, output, and total tokens, counted
across everything the run does.

## When to use

Untrusted prompts, tools that trigger long exchanges, cost-sensitive
production — anywhere a runaway loop must stop.

## When a simpler bound suffices

`WithMaxIterations` already caps model turns per run; reach for usage
limits when the bound must be about tokens or spend, not turns.

## How it works

`golem.UsageLimit` carries three independent bounds; each zero value
means unbounded and the zero struct disables the limit entirely. Usage is
counted cumulatively across every model turn, retried call, and
correction round, and checked after each model response: the response
that crosses a bound fails the run at the `usage` stage — even when it
would have decoded successfully. The failure wraps a typed
`golem.UsageLimitError` naming the crossed dimension, its bound, and the
run's actual usage.

## Example

See `ExampleWithUsageLimit` in package docs:

```go
agent, _ := golem.New[struct{}, string](client, decoder,
    golem.WithUsageLimit[struct{}, string](golem.UsageLimit{TotalTokens: 10_000}),
)

_, err := agent.Run(ctx, runCtx, prompt)
var usageErr *golem.UsageLimitError
if errors.As(err, &usageErr) {
    // stop, alert, or degrade — the run did not finish
}
```

## API surface

- `golem.WithUsageLimit[Deps, Output](limit UsageLimit)`
- `golem.UsageLimit{InputTokens, OutputTokens, TotalTokens int}`
- `golem.UsageLimitError{Kind, Limit, Actual}` via `RunError{Stage: StageUsage}`
- `golem.StageUsage`

## Gotchas

- Providers that do not report usage count as zero tokens — a limit never
  trips without provider-reported usage.
- Negative values fail construction.
- The check is post-response by design: one response may overshoot the
  bound before the run stops.
