# Usage limits

## Purpose

Bound what one run can consume: input, output, and total tokens, plus the
model requests and tool executions it performs, counted across everything
the run does.

## When to use

Untrusted prompts, tools that trigger long exchanges, cost-sensitive
production — anywhere a runaway loop must stop.

## When a simpler bound suffices

`WithMaxIterations` already caps model turns per run; reach for usage
limits when the bound must be about tokens or spend, not turns.

## How it works

`golem.UsageLimit` carries five independent bounds; each zero value
means unbounded and the zero struct disables the limit entirely. Usage is
counted cumulatively across every model turn, retried call, and
correction round, and checked after each model response: the response
that crosses a bound fails the run at the `usage` stage — even when it
would have decoded successfully. The failure wraps a typed
`golem.UsageLimitError` naming the crossed dimension, its bound, and the
run's actual usage, and `RunError.Partial` preserves the conversation
including the crossing response, the usage it reported, and the run's
request and tool counts.

Tokens come from provider-reported usage. Requests and tool executions
are counted by the run itself: a request is one provider call, retried
attempts included, and a tool execution is one tool run — rejected calls
count, because the tool ran; unknown-tool requests and interrupted
output-tool co-emissions do not.

The run surfaces those counts on every result, successful or not:
`Result.Requests` and `Result.ToolCalls` carry the same numbers the
limit checks and `RunError.Partial` preserves, so a cost ledger reads
them off the result instead of inferring activity from messages. The
counts cover one run only — a delegated sub-agent's activity stays in
the sub-agent's own result (see the agent delegation guide).

## Example

See `ExampleWithUsageLimit` in package docs:

```go
agent, _ := golem.New[struct{}, string](client, decoder,
    golem.WithUsageLimit[struct{}, string](golem.UsageLimit{TotalTokens: 10_000}),
)

result, err := agent.Run(ctx, runCtx, prompt)
var usageErr *golem.UsageLimitError
if errors.As(err, &usageErr) {
    // stop, alert, or degrade — the run did not finish
}
log.Printf("run used %d input and %d output tokens across %d requests and %d tool calls",
    result.Usage.InputTokens, result.Usage.OutputTokens, result.Requests, result.ToolCalls)
```

## API surface

- `golem.WithUsageLimit[Deps, Output](limit UsageLimit)`
- `golem.UsageLimit{InputTokens, OutputTokens, TotalTokens, Requests, ToolCalls int}`
- `golem.UsageLimitError{Kind, Limit, Actual}` via `RunError{Stage: StageUsage}`
- `golem.StageUsage`
- `golem.Result.Requests`, `golem.Result.ToolCalls` — the counts a
  successful (or paused) run performed, shared with the limit's
  accounting; `golem.PartialResult.Requests`, `golem.PartialResult.ToolCalls`
  on failure

## Gotchas

- Providers that do not report usage count as zero tokens — a token limit
  never trips without provider-reported usage. Request and tool-call
  bounds count locally and always apply.
- Negative values fail construction.
- The check is post-response by design: one response may overshoot the
  bound before the run stops.
