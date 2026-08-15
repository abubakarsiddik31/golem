// Package golem provides typed building blocks for AI agents in Go.
package golem

import (
	"context"
	"fmt"

	"github.com/abubakarsiddik31/golem/model"
)

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

// Agent combines a model, instructions, and a typed output boundary. Deps is
// declared now so the future tool API can receive application dependencies
// without untyped maps or ambient globals.
type Agent[Deps any, Output any] struct {
	model        model.Model
	decoder      OutputDecoder[Output]
	instructions string
}

// Option configures an Agent during construction.
type Option[Deps any, Output any] func(*Agent[Deps, Output])

// WithInstructions configures stable system instructions for every run.
func WithInstructions[Deps any, Output any](instructions string) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.instructions = instructions
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
		model:   modelClient,
		decoder: decoder,
	}
	for _, option := range options {
		if option != nil {
			option(agent)
		}
	}
	return agent, nil
}

// RunContext carries explicit application dependencies for a run. The first
// version does not consume Deps itself; retaining it here makes the dependency
// contract stable as tool execution is introduced.
type RunContext[Deps any] struct {
	Deps Deps
}

// Result preserves the typed output and the normalized model evidence that
// produced it. This makes testing and observability possible without a tracing
// backend.
type Result[Output any] struct {
	Output   Output
	Messages []model.Message
	Usage    model.Usage
}

// Run asks the configured model to answer prompt and decodes its response.
func (a *Agent[Deps, Output]) Run(ctx context.Context, runCtx RunContext[Deps], prompt string) (Result[Output], error) {
	_ = runCtx // Tools will consume explicit dependencies in a subsequent iteration.

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

	response, err := a.model.Generate(ctx, request)
	if err != nil {
		return Result[Output]{}, &RunError{Stage: StageModel, Err: err}
	}

	output, err := a.decoder.Decode(ctx, response)
	if err != nil {
		return Result[Output]{}, &RunError{Stage: StageDecode, Err: err}
	}

	return Result[Output]{
		Output:   output,
		Messages: append(request.Messages, response.Message),
		Usage:    response.Usage,
	}, nil
}
