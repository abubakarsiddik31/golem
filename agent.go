// Package golem provides typed building blocks for AI agents in Go.
package golem

import (
	"context"
	"errors"
	"fmt"

	"github.com/abubakarsiddik31/golem/internal/runner"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// DefaultMaxIterations bounds model turns per run when no explicit limit is
// configured (ADR 0002).
const DefaultMaxIterations = 10

// OutputDecoder validates and converts a provider response to the agent's
// declared result type. It is the boundary at which model-produced data becomes
// application data.
type OutputDecoder[Output any] interface {
	Decode(ctx context.Context, response model.Response) (Output, error)
}

// DecodeFunc adapts a function to an OutputDecoder.
type DecodeFunc[Output any] func(context.Context, model.Response) (Output, error)

// Decode converts response using f.
func (f DecodeFunc[Output]) Decode(ctx context.Context, response model.Response) (Output, error) {
	return f(ctx, response)
}

// Agent combines a model, instructions, tools, and a typed output boundary.
// Deps is the dependency value tools receive on every run.
type Agent[Deps any, Output any] struct {
	model         model.Model
	decoder       OutputDecoder[Output]
	instructions  string
	tools         []tool.Tool[Deps]
	maxIterations int
}

// Option configures an Agent during construction.
type Option[Deps any, Output any] func(*Agent[Deps, Output])

// WithInstructions configures stable system instructions for every run.
func WithInstructions[Deps any, Output any](instructions string) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.instructions = instructions
	}
}

// WithTools registers tools the model may request. Tools should be built
// with tool.New; New rejects invalid or duplicate declarations.
func WithTools[Deps any, Output any](tools ...tool.Tool[Deps]) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.tools = append(agent.tools, tools...)
	}
}

// WithMaxIterations bounds model turns per run. It must be at least 1;
// otherwise New fails.
func WithMaxIterations[Deps any, Output any](iterations int) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.maxIterations = iterations
	}
}

// New creates an Agent. A model and decoder are both required: Golem never
// guesses how untrusted model output becomes a typed application value.
func New[Deps any, Output any](
	modelClient model.Model,
	decoder OutputDecoder[Output],
	options ...Option[Deps, Output],
) (*Agent[Deps, Output], error) {
	if modelClient == nil {
		return nil, fmt.Errorf("golem: model is required")
	}
	if decoder == nil {
		return nil, fmt.Errorf("golem: output decoder is required")
	}

	agent := &Agent[Deps, Output]{
		model:         modelClient,
		decoder:       decoder,
		maxIterations: DefaultMaxIterations,
	}
	for _, option := range options {
		if option != nil {
			option(agent)
		}
	}
	if agent.maxIterations < 1 {
		return nil, fmt.Errorf("golem: max iterations must be at least 1, got %d", agent.maxIterations)
	}
	if err := validateTools(agent.tools); err != nil {
		return nil, err
	}
	return agent, nil
}

func validateTools[Deps any](tools []tool.Tool[Deps]) error {
	seen := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		if t.Name == "" {
			return fmt.Errorf("golem: tool name is required")
		}
		if t.Exec == nil {
			return fmt.Errorf("golem: tool %q: exec function is required", t.Name)
		}
		if _, duplicate := seen[t.Name]; duplicate {
			return fmt.Errorf("golem: tool %q: duplicate name", t.Name)
		}
		seen[t.Name] = struct{}{}
	}
	return nil
}

// RunContext carries explicit application dependencies for a run. Its Deps
// value flows to every tool executed during the run.
type RunContext[Deps any] struct {
	Deps Deps
}

// Result preserves the typed output and the normalized model evidence that
// produced it, including every tool-call exchange in execution order. This
// makes testing and observability possible without a tracing backend.
type Result[Output any] struct {
	Output   Output
	Messages []model.Message
	Usage    model.Usage
}

// Run executes the agent: it asks the configured model to answer prompt,
// executing requested tools along the way, and decodes the final response.
//
// Errors are wrapped in RunError with the failing stage. Cancellation and
// deadline errors are returned unwrapped so callers can match them
// directly with errors.Is.
func (a *Agent[Deps, Output]) Run(ctx context.Context, runCtx RunContext[Deps], prompt string) (Result[Output], error) {
	request := model.Request{Messages: make([]model.Message, 0, 2)}
	if a.instructions != "" {
		request.Messages = append(request.Messages, model.Message{
			Role:    model.RoleSystem,
			Content: a.instructions,
		})
	}
	request.Messages = append(request.Messages, model.Message{
		Role:    model.RoleUser,
		Content: prompt,
	})
	for _, t := range a.tools {
		request.ToolSpecs = append(request.ToolSpecs, model.ToolSpec{
			Name:        t.Name,
			Description: t.Description,
			Schema:      t.Schema,
		})
	}

	outcome, err := runner.Execute(ctx, a.model, a.tools, runCtx.Deps, request, a.maxIterations)
	if err != nil {
		return Result[Output]{}, classifyRunError(err)
	}

	output, err := a.decoder.Decode(ctx, outcome.Response)
	if err != nil {
		return Result[Output]{}, &RunError{Stage: StageDecode, Err: err}
	}

	return Result[Output]{
		Output:   output,
		Messages: outcome.Messages,
		Usage:    outcome.Usage,
	}, nil
}

// classifyRunError maps runner outcomes to public stages. Cancellation and
// deadline errors stay unwrapped so callers can match them with errors.Is.
func classifyRunError(err error) error {
	var toolErr *runner.ToolError
	switch {
	case errors.As(err, &toolErr):
		return &RunError{Stage: StageTool, Err: err}
	case errors.Is(err, runner.ErrLoopLimit):
		return &RunError{Stage: StageLoop, Err: err}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return &RunError{Stage: StageModel, Err: err}
	}
}
