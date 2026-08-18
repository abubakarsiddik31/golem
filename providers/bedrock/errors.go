package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// APIError reports a provider-side failure with an HTTP status. It
// implements model.RetryableError: Retryable is true for 408, 429, and 5xx
// responses — the classification the runner retry policy consumes (which
// covers ModelTimeoutException, ThrottlingException, ModelNotReadyException,
// InternalServerException, and ServiceUnavailableException). The adapter
// itself never retries.
type APIError struct {
	StatusCode int
	// Code is the provider exception name, e.g. "ThrottlingException",
	// read from the x-amzn-errortype header.
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
		return fmt.Sprintf("bedrock: status %d (%s): %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("bedrock: status %d: %s", e.StatusCode, e.Message)
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
	return fmt.Sprintf("bedrock: transport error: %v", e.Err)
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
	return fmt.Sprintf("bedrock: %s: %v", e.Stage, e.Err)
}

// Unwrap exposes the underlying encoding/json or shape error.
func (e *DecodeError) Unwrap() error {
	return e.Err
}

// errorBody is the Converse API error payload.
type errorBody struct {
	Message string `json:"message"`
}

// newAPIError classifies a provider failure. The exception type travels
// in the x-amzn-errortype header, optionally suffixed with a subtype.
func newAPIError(statusCode int, payload []byte, errorTypeHeader string) *APIError {
	apiError := &APIError{
		StatusCode: statusCode,
		Code:       trimErrorType(errorTypeHeader),
		Message:    http.StatusText(statusCode),
		retryable:  statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests || statusCode >= 500,
	}
	var body errorBody
	if err := json.Unmarshal(payload, &body); err == nil && body.Message != "" {
		apiError.Message = body.Message
	}
	return apiError
}

func trimErrorType(errorType string) string {
	if i := strings.IndexAny(errorType, ":;"); i >= 0 {
		return errorType[:i]
	}
	return errorType
}

// ErrUnsupportedContent reports a request carrying content the Converse
// API cannot express, such as an image referenced by URL: Converse
// accepts inline image bytes only. Fetch the content and attach it inline
// (golem.WithPromptImageData). Errors wrapping this sentinel are
// generated before any request is sent; they are never retryable.
var ErrUnsupportedContent = errors.New("bedrock: content not supported by the Converse API")
