package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// APIError reports a provider-side failure with an HTTP status. It
// implements model.RetryableError: Retryable is true for 408, 429, and 5xx
// responses — the classification the runner retry policy consumes. The
// adapter itself never retries.
type APIError struct {
	StatusCode int
	// Code is the provider-reported status string, when present, e.g.
	// "UNAVAILABLE".
	Code string
	// Message is the provider-reported message or a generic status line.
	Message string
	// retryable marks statuses a caller may reasonably retry.
	retryable bool
}

// Retryable reports whether the failed request may be attempted again.
func (e *APIError) Retryable() bool {
	return e.retryable
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("gemini: status %d (%s): %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("gemini: status %d: %s", e.StatusCode, e.Message)
}

// TransportError reports a network-level failure: the request could not be
// delivered or its response could not be read. It implements
// model.RetryableError; such failures are retryable unless the cause is
// context cancellation or a deadline, because retrying a canceled operation
// would fight the caller's decision to stop.
type TransportError struct {
	// Err is the underlying net/http error.
	Err error
}

func (e *TransportError) Error() string {
	return fmt.Sprintf("gemini: transport error: %v", e.Err)
}

// Unwrap exposes the underlying transport error.
func (e *TransportError) Unwrap() error {
	return e.Err
}

// Retryable reports whether the failed request may be attempted again.
func (e *TransportError) Retryable() bool {
	return !errors.Is(e.Err, context.Canceled) && !errors.Is(e.Err, context.DeadlineExceeded)
}

// DecodeError reports an unexpected request-encoding or response-decoding
// failure. It is never retryable: the input or the provider payload does
// not match the expected shape.
type DecodeError struct {
	Stage string
	Err   error
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("gemini: %s: %v", e.Stage, e.Err)
}

// Unwrap exposes the underlying encoding/json or shape error.
func (e *DecodeError) Unwrap() error {
	return e.Err
}

// errorEnvelope is the GenerateContent API error body.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

func newAPIError(statusCode int, payload []byte) *APIError {
	apiError := &APIError{
		StatusCode: statusCode,
		Message:    http.StatusText(statusCode),
		retryable:  statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests || statusCode >= 500,
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(payload, &envelope); err == nil && envelope.Error.Message != "" {
		apiError.Message = envelope.Error.Message
		apiError.Code = envelope.Error.Status
	}
	return apiError
}
