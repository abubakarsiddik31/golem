package model

import (
	"context"
	"errors"
	"fmt"
)

// ModelRetry reports that a model response failed validation in a way the
// model can correct if asked (ADR 0006). Decoders return it — typically
// wrapped — to reject a response; the agent appends the failure to the
// conversation and asks the model again, bounded by the run's output retry
// budget. It is a content signal, not a transport one: it never classifies
// as transport-retryable.
type ModelRetry struct {
	// Err describes why the response was rejected.
	Err error
}

func (e *ModelRetry) Error() string {
	return fmt.Sprintf("model output rejected: %v", e.Err)
}

// Unwrap exposes the rejection reason.
func (e *ModelRetry) Unwrap() error {
	return e.Err
}

// RetryableError is implemented by model errors that report whether the
// failed generation can reasonably be attempted again, such as an adapter's
// classification of HTTP 429 and 5xx responses (ADR 0004). The runner's
// retry policy inspects errors through this contract; the core never learns
// provider-specific failure shapes.
type RetryableError interface {
	error
	// Retryable reports whether a caller may retry the failed operation.
	Retryable() bool
}

// IsRetryable reports whether err, or any error in its chain, classifies as
// retryable. Context cancellation and deadline errors are never retryable,
// whatever else the chain contains: retrying a canceled operation must not
// fight the caller's decision to stop.
func IsRetryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var retryable RetryableError
	return errors.As(err, &retryable) && retryable.Retryable()
}
