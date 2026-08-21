# Run events

## Purpose

Observe a run while it executes: every provider call attempt — retried
attempts included —, every tool execution, and every decoder correction
boundary, delivered as typed events in execution order.

## When to use

Structured logging, tracing spans, progress reporting, or metering that
needs to see a run as it happens rather than after it ends. Not when
post-hoc inspection is enough — `Result.Messages` remains the canonical
record, and text streaming already covers showing generation progress.

## How it works

`WithRunEvents` registers one observer per agent. The run invokes it
synchronously on the caller's goroutine, inline with execution: no
goroutines, no buffers, no dropped events. The observer returns nothing,
so observation can never fail a run; an observer that must stop the run
cancels the run context. `Run`, `RunWithHistory`, and both streaming
variants emit the same events.

Events arrive in deterministic execution order, and that order is a
compatibility promise:

- `model_start` / `model_end` bracket every provider call attempt,
  carrying the turn index and 1-based attempt number; `model_end` carries
  the attempt's usage and error. A retried attempt is individually
  observable.
- `tool_start` / `tool_end` bracket every tool execution, carrying the
  call ID, tool name, raw arguments, and on end the result text or error.
  A correction rejection arrives as a `tool_end` whose error is a
  `*model.ModelRetry`.
- `output_rejected` marks a decoder rejection starting a correction
  round; its `Attempt` numbers the round that follows, and turn indexes
  restart with it.

Parallel tool groups stay deterministic: starts are emitted in model
emission order before the group runs, ends in the same order after it
completes — matching the result-evidence order rule.

## Example

Run `examples/run-events` (offline, against a scripted fake model):

```bash
go run ./examples/run-events
```

```go
agent, err := golem.New[string, string](client,
	golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
		return response.Message.Content, nil
	}),
	golem.WithTools[string, string](getPlayerName),
	golem.WithRunEvents[string, string](func(event golem.RunEvent) {
		fmt.Printf("%-16s turn=%d attempt=%d\n", event.Kind, event.Turn, event.Attempt)
	}),
)
```

## API surface

- `golem.WithRunEvents[Deps, Output](onEvent func(RunEvent)) Option[Deps, Output]`
- `golem.RunEvent{Kind, Turn, Attempt, CallID, ToolName, Args, Result, Err, Usage}`
- `golem.EventKind` — `golem.EventModelStart`, `golem.EventModelEnd`,
  `golem.EventToolStart`, `golem.EventToolEnd`, `golem.EventOutputRejected`

## Gotchas

- The observer runs inline with model and tool execution: it must not
  block. Cancel the run context — don't stall the callback — to stop the
  run.
- Events are advisory observation; usage bounds and the canonical record
  come from the run `Result`. A streaming run emits the same events
  alongside its fragments, with a model turn's events bracketing its
  fragments.
- Which fields carry meaning depends on the kind; the rest are zero.
- Decisions live in `docs/adr/0014-run-event-stream.md`.
