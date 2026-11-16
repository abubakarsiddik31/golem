# Loop detection

## Purpose

Stop a run that keeps requesting the same tool call — same tool, same
arguments — before the repeated call executes and before it duplicates a
side effect.

## When to use

Autonomous agents, bots, and polling tools where a stuck model can call
the same tool indefinitely. Deliberate polling should raise the bound or
leave detection off; a run that is supposed to poll can use changing
arguments or its own state to stay under the check.

## How it works

`WithLoopDetection(maxRepeats)` counts tool calls by name and canonical
JSON arguments across the whole run. Object key order does not matter:
`{"a":1,"b":2}` and `{"b":2,"a":1}` are the same call. When a call would
make the count exceed `maxRepeats`, the run fails **before that call
executes**, with `RunError.Stage == StageLoopDetected` wrapping a
`*LoopDetectedError`. The error carries the tool name, the offending raw
arguments, the count including the failed call, and the configured bound.

The failure keeps the partial conversation through the assistant turn
that requested the repeated call, so `RunError.Partial` is resumable. The
offending call never runs, so a repeated side effect cannot be applied by
the check itself. Detection is off by default; zero disables it, and
negative values fail `New`.

This is deliberately narrower than Pydantic AI Harness's detect-loops
capability: v1 checks repeated calls only, across the whole run rather
than a sliding window, and stops rather than nudging. It compares
arguments, not results or response text.

## Example

`examples/loop-detection` runs offline and stops on the fourth identical
poll with a limit of three:

```go
agent, err := golem.New[struct{}, string](client, decoder,
	golem.WithTools[struct{}, string](poll),
	golem.WithLoopDetection[struct{}, string](3),
)
_, err = agent.Run(ctx, golem.RunContext[struct{}]{}, "Wait for job-42.")

var runErr *golem.RunError
var loop *golem.LoopDetectedError
errors.As(err, &runErr) // runErr.Stage == golem.StageLoopDetected
errors.As(err, &loop)   // loop.ToolName, loop.Count, loop.Threshold, loop.Args
```

## API surface

- `golem.WithLoopDetection[Deps, Output](maxRepeats int)`
- `golem.StageLoopDetected`
- `golem.LoopDetectedError{ToolName, Args, Count, Threshold}`

## Gotchas

- The count spans the whole run. A long run that legitimately calls the
  same tool many times needs a higher bound or distinct arguments.
- A rejected, blocked, or deferred call still counts as a request; the
  check is about what the model asked for, not what executed.
- Results are not compared. A tool returning different text for the same
  arguments still trips the call check.
- Detection stops the run; it does not nudge the model. Pair it with
  `WithUsageLimit` if you also want token or request budgets, and with a
  retry/fallback policy if the application should recover.
- Invalid JSON arguments fall back to raw-byte comparison rather than
  hiding a repeat.
- The deciding design record is `docs/adr/0051-loop-detection.md`.
