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

`WithLoopDetection(maxRepeats)` is the shorthand for repeated identical
tool calls across the whole run. `WithLoopPolicy` configures the broader
checks:

| Pattern | Fires when |
| --- | --- |
| `repeated_call` | the same tool call (name and canonical arguments) appears more than the threshold in `Window` calls |
| `repeated_result` | the same call returns the same result more than the threshold |
| `alternating_calls` | two different calls alternate A-B-A-B for the configured number of cycles |
| `repeated_text` | the same final text repeats more than the threshold |

Object key order does not matter: `{"a":1,"b":2}` and
`{"b":2,"a":1}` are the same call. Thresholds are the number of
repetitions allowed; the next one fires. `Window` bounds the call-based
checks (zero uses 10). Each threshold is disabled at zero and must be at
least 2 when set. `WithLoopDetection` and `WithLoopPolicy` are mutually
exclusive.

`LoopStop` ends the run with `RunError.Stage == StageLoopDetected`
wrapping `*LoopDetectedError`, which carries the pattern, tool names,
offending arguments, count, and threshold. For a pre-execution call
pattern, the offending call never runs. `LoopNudge` instead feeds a
course-correction back to the model: pre-execution call patterns get
failed tool results saying the call was not executed, then a user-role
nudge, and the run continues. Repeated-result checks run after the result
is recorded; repeated-text checks run on the final response. Nudges are
bounded by the run's iteration and usage limits.

The failure keeps the partial conversation through the assistant turn
that requested the call, so `RunError.Partial` is resumable. Detection is
off by default; zero disables it, and negative values fail `New`.

## Example

`examples/loop-detection` runs offline and stops on the fourth identical
poll with a limit of three:

```go
agent, err := golem.New[struct{}, string](client, decoder,
	golem.WithTools[struct{}, string](poll),
	golem.WithLoopDetection[struct{}, string](3),
)

// Or configure patterns and a nudge:
golem.WithLoopPolicy[struct{}, string](golem.LoopPolicy{
	Window: 10, RepeatedResult: 3, AlternatingCalls: 3, Action: golem.LoopNudge,
})
_, err = agent.Run(ctx, golem.RunContext[struct{}]{}, "Wait for job-42.")

var runErr *golem.RunError
var loop *golem.LoopDetectedError
errors.As(err, &runErr) // runErr.Stage == golem.StageLoopDetected
errors.As(err, &loop)   // loop.ToolName, loop.Count, loop.Threshold, loop.Args
```

## API surface

- `golem.WithLoopDetection[Deps, Output](maxRepeats int)` — repeated-call shorthand
- `golem.WithLoopPolicy[Deps, Output](golem.LoopPolicy)` — patterns, window, action
- `golem.LoopPolicy{Window, RepeatedCall, RepeatedResult, AlternatingCalls, RepeatedText, Action}`
- `golem.LoopAction` — `LoopStop | LoopNudge`
- `golem.LoopPattern` — `LoopRepeatedCall | LoopRepeatedResult | LoopAlternatingCalls | LoopRepeatedText`
- `golem.StageLoopDetected`
- `golem.LoopDetectedError{Pattern, ToolNames, ToolName, Args, Count, Threshold}`

## Gotchas

- The count spans the whole run. A long run that legitimately calls the
  same tool many times needs a higher bound or distinct arguments.
- A rejected, blocked, or deferred call still counts as a request; the
  check is about what the model asked for, not what executed.
- Results are not compared. A tool returning different text for the same
  arguments still trips the call check.
- `LoopNudge` does not guarantee progress: a model that ignores the
  nudge keeps going until `WithMaxIterations` or a usage limit stops it.
  `LoopStop` is the hard guarantee.
- A pre-execution nudge closes the requested calls with failed results
  and appends a user-role nudge; those calls do not count as executed
  tool activity.
- Repeated-result detection needs the result, so the offending call has
  already run when it fires. Side effects are not rolled back.
- Repeated-text detection is meaningful when a nudge or output-correction
  round produces the same final answer again; a normal run ends at the
  first final answer.
- Invalid JSON arguments fall back to raw-byte comparison rather than
  hiding a repeat.
- The deciding design record is `docs/adr/0051-loop-detection.md`.
