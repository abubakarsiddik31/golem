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
)

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
	// Exec runs the tool and returns the text handed back to the model. It
	// must honor ctx cancellation and return a classified error rather than
	// logging it. Returning an error that wraps *model.ModelRetry rejects
	// this call as correctable: with a tool retry budget configured, the
	// run feeds the rejection back to the model.
	Exec func(ctx context.Context, deps Deps, args json.RawMessage) (string, error)
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
