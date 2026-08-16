package testmodel

import (
	"context"

	"github.com/abubakarsiddik31/golem/model"
)

// Func adapts a function to a plain model.Model: the function receives the
// exact normalized request the agent built and decides the response, which
// is the direct way to script behavior that reacts to the conversation.
// Func deliberately does not stream — asserting that a model without the
// streaming capability is rejected is also worth testing.
type Func func(ctx context.Context, request model.Request) (model.Response, error)

var _ model.Model = Func(nil)

// Generate delegates to f.
func (f Func) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	return f(ctx, request)
}

// StreamFunc adapts a streaming function to model.StreamingModel.
// Generate calls f with a nil onDelta, so f must forward fragments through
// Emit, which discards them when onDelta is nil.
type StreamFunc func(ctx context.Context, request model.Request, onDelta func(model.Delta) error) (model.Response, error)

var _ model.StreamingModel = StreamFunc(nil)

// GenerateStream delegates to f.
func (f StreamFunc) GenerateStream(ctx context.Context, request model.Request, onDelta func(model.Delta) error) (model.Response, error) {
	return f(ctx, request, onDelta)
}

// Generate calls f with a nil onDelta.
func (f StreamFunc) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	return f(ctx, request, nil)
}

// Emit forwards delta to onDelta, discarding it when onDelta is nil. Use
// it inside function fakes so they tolerate both call shapes.
func Emit(onDelta func(model.Delta) error, delta model.Delta) error {
	if onDelta == nil {
		return nil
	}
	return onDelta(delta)
}
