// Package tool defines Golem's typed tool declaration and execution
// contract. A tool describes itself with inspectable metadata and executes
// with the caller's context, the run's dependency value, and raw
// model-produced arguments.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/abubakarsiddik31/golem/model"
)

// Result is what one tool execution hands back to the model. Text is the
// conventional tool result; Parts attach non-text evidence — images,
// documents, audio, video — to the same call. Where a provider cannot
// carry a part inside its tool-result channel, the adapter frames the
// part onto the user channel with tags naming the call, or fails before
// the request when it has no way to send it at all; either way the
// decision is documented per adapter and never silently drops content.
type Result struct {
	// Text is the tool result text the model reads beside any parts.
	Text string
	// Parts is optional non-text evidence produced by this call. Parts
	// must be well-formed (see model.Part.Validate); a malformed part
	// fails the run at the tool stage.
	Parts []model.Part
}

// Text returns a text-only Result, the shape a pre-parts tool returned.
func Text(s string) Result {
	return Result{Text: s}
}

// Failed is a definitive tool failure: the call completed but produced no
// usable outcome — a missing resource, an unsupported operation, an
// upstream error. Returning it records the failure as the tool's result
// so the model sees it and decides what to do next; the run continues and
// the tool's retry budget is untouched. Use *model.ModelRetry instead
// when the model should correct the call and try again.
type Failed struct {
	// Reason is the model-visible failure description.
	Reason string
}

func (f *Failed) Error() string {
	return "tool failed: " + f.Reason
}

// Tool is one executable capability offered to a model. Deps is the agent's
// declared dependency type; the same value flows to every tool in a run.
//
// Args is untrusted model output passed through as raw JSON. Decoding and
// validating it is the tool author's explicit job; Golem does not use
// reflection to infer schemas or arguments.
type Tool[Deps any] struct {
	Name        string
	Description string
	// Schema is a JSON Schema document describing the arguments object.
	Schema json.RawMessage
	// Exec runs the tool and returns the result handed back to the model —
	// text plus optional non-text parts. It must honor ctx cancellation and
	// return a classified error rather than logging it. Returning an error
	// that wraps *model.ModelRetry rejects this call as correctable: with a
	// tool retry budget configured, the run feeds the rejection back to the
	// model. Returning &tool.Failed records the failure as the tool's
	// result — the model sees it, the run continues, the retry budget is
	// untouched.
	Exec func(ctx context.Context, deps Deps, args json.RawMessage) (Result, error)
	// MaxRetries overrides the agent's tool-rejection budget for this tool.
	// Nil inherits the agent setting; a pointer to zero permits no correction.
	MaxRetries *int
	// Timeout bounds one execution when non-zero. It overrides the agent's
	// default tool timeout and is enforced by cancelling the execution context.
	Timeout time.Duration
	// Sequential makes this tool a barrier when parallel tool calls are enabled.
	// Calls before it finish first; it runs alone; later calls start afterward.
	Sequential bool
}

// New validates t and returns it ready for registration with an agent.
// Construction fails on a missing name, invalid Schema JSON, or a nil Exec
// so that a misdeclared tool can never reach a model.
func New[Deps any](t Tool[Deps]) (Tool[Deps], error) {
	if t.Name == "" {
		return Tool[Deps]{}, fmt.Errorf("tool: name is required")
	}
	if len(t.Schema) == 0 {
		return Tool[Deps]{}, fmt.Errorf("tool %q: schema is required", t.Name)
	}
	if !json.Valid(t.Schema) {
		return Tool[Deps]{}, fmt.Errorf("tool %q: schema is not valid JSON", t.Name)
	}
	if t.Exec == nil {
		return Tool[Deps]{}, fmt.Errorf("tool %q: exec function is required", t.Name)
	}
	if t.MaxRetries != nil && *t.MaxRetries < 0 {
		return Tool[Deps]{}, fmt.Errorf("tool %q: max retries must not be negative, got %d", t.Name, *t.MaxRetries)
	}
	if t.Timeout < 0 {
		return Tool[Deps]{}, fmt.Errorf("tool %q: timeout must not be negative, got %s", t.Name, t.Timeout)
	}
	return t, nil
}

// RetryLimit returns a per-tool retry limit suitable for Tool.MaxRetries.
// A limit of zero disallows correction for that tool.
func RetryLimit(retries int) *int {
	return &retries
}

// MustNew is New for tools declared as package-level or test fixtures; it
// panics on invalid configuration.
func MustNew[Deps any](t Tool[Deps]) Tool[Deps] {
	validated, err := New(t)
	if err != nil {
		panic(err)
	}
	return validated
}
