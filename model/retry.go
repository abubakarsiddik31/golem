package model

import (
	"context"
	"errors"
)

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
