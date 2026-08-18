package model

import (
	"context"
	"fmt"
)

// Fallback is a Model that tries its models in order: the primary first,
// then each alternate, moving on only when a model fails with a
// retryable-classified error (see IsRetryable). The first success returns
// immediately; a non-retryable failure returns immediately, unchanged.
// When every model fails, the last model's error returns unchanged so its
// classification survives for the caller's retry policy. Failures skipped
// on the way to an answer are not surfaced.
//
// Context cancellation always wins: after any failure, a canceled context
// returns the context error instead of trying the next model.
type Fallback struct {
	models []Model
}

// NewFallback builds a Fallback from a primary model and at least one
// alternate. Both are required and must be non-nil.
func NewFallback(primary Model, alternates ...Model) (*Fallback, error) {
	if primary == nil {
		return nil, fmt.Errorf("model: fallback primary is required")
	}
	if len(alternates) == 0 {
		return nil, fmt.Errorf("model: fallback requires at least one alternate model")
	}
	models := make([]Model, 0, 1+len(alternates))
	models = append(models, primary)
	for i, alternate := range alternates {
		if alternate == nil {
			return nil, fmt.Errorf("model: fallback alternate %d is nil", i)
		}
		models = append(models, alternate)
	}
	return &Fallback{models: models}, nil
}

// Generate answers with the first model that succeeds, per the Fallback
// contract.
func (f *Fallback) Generate(ctx context.Context, request Request) (Response, error) {
	for i, m := range f.models {
		response, err := m.Generate(ctx, request)
		if err == nil {
			return response, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Response{}, ctxErr
		}
		if !IsRetryable(err) || i == len(f.models)-1 {
			return Response{}, err
		}
	}
	return Response{}, fmt.Errorf("model: fallback exhausted") // unreachable
}

// GenerateStream streams from the first model that succeeds, with one
// limit: it falls back only when a model fails before forwarding its
// first fragment. Once any fragment was forwarded, falling back would
// replay fragments the caller has already seen, so the error returns
// as-is. Every member must implement StreamingModel; a member that does
// not is a configuration error, not a fallback candidate.
func (f *Fallback) GenerateStream(ctx context.Context, request Request, onDelta func(Delta) error) (Response, error) {
	// Validate every member up front: Fallback satisfies StreamingModel
	// unconditionally, so a silent member would surface only when a
	// fallback reached it.
	streamers := make([]StreamingModel, len(f.models))
	for i, m := range f.models {
		streamer, ok := m.(StreamingModel)
		if !ok {
			return Response{}, fmt.Errorf("model: fallback member %T does not support streaming", m)
		}
		streamers[i] = streamer
	}
	emitted := false
	forward := func(delta Delta) error {
		emitted = true
		if onDelta == nil {
			return nil
		}
		return onDelta(delta)
	}
	for i, streamer := range streamers {
		response, err := streamer.GenerateStream(ctx, request, forward)
		if err == nil {
			return response, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Response{}, ctxErr
		}
		if emitted || !IsRetryable(err) || i == len(f.models)-1 {
			return Response{}, err
		}
	}
	return Response{}, fmt.Errorf("model: fallback exhausted") // unreachable
}
