package golem

import (
	"github.com/abubakarsiddik31/golem/internal/runner"
)

// RunEvent is one observation of an executing run: a provider call
// attempt, a tool execution, or a correction boundary. Events are
// delivered synchronously and in deterministic execution order — the
// contract and its ordering rules live with the execution loop and are
// re-exported here.
type RunEvent = runner.Event

// EventKind identifies which RunEvent fields carry meaning.
type EventKind = runner.EventKind

const (
	// EventModelStart precedes one provider call attempt.
	EventModelStart = runner.EventModelStart
	// EventModelEnd follows one provider call attempt, carrying the
	// attempt's usage and error.
	EventModelEnd = runner.EventModelEnd
	// EventToolStart precedes one tool execution.
	EventToolStart = runner.EventToolStart
	// EventToolEnd follows one tool execution, carrying the result or the
	// error; a correction rejection carries a *model.ModelRetry error.
	EventToolEnd = runner.EventToolEnd
	// EventOutputRejected marks a decoder rejection starting a correction
	// round; its Attempt numbers the round that follows, and turn numbers
	// restart with it.
	EventOutputRejected = runner.EventOutputRejected
	// EventDeferred marks a tool call that deferred instead of executing;
	// the run pauses with the call pending on Result.Pending. It replaces
	// the call's tool-end event.
	EventDeferred = runner.EventDeferred
)

// WithRunEvents registers an observer invoked for every observable point
// of each run: provider call attempts — retried attempts included —,
// tool executions, and decoder correction boundaries. The observer runs
// inline with execution: it must not block, it cannot fail the run, and
// an observer that must stop the run cancels the run context. Run and
// its history and streaming variants emit the same events. Events are
// advisory observation; the canonical record remains the run Result.
func WithRunEvents[Deps any, Output any](onEvent func(RunEvent)) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.runEvents = onEvent
	}
}
