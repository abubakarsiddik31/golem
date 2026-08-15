package openai

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// APIError reports a provider-side failure with an HTTP status. Retryable
// is true for 408, 429, and 5xx responses: the classification future
// runner retry policies consume. The adapter itself never retries.
type APIError struct {
	StatusCode int
	// Code is the provider-reported error code, when present.
	Code string
	// Message is the provider-reported message or a generic status line.
	Message string
	// Retryable marks statuses a caller may reasonably retry.
	Retryable bool
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("openai: status %d (%s): %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("openai: status %d: %s", e.StatusCode, e.Message)
}

// DecodeError reports an unexpected request-encoding or response-decoding
// failure. It is never retryable: the input or the provider payload does
// not match the expected shape.
type DecodeError struct {
	Stage string
	Err   error
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("openai: %s: %v", e.Stage, e.Err)
}

// Unwrap exposes the underlying encoding/json or shape error.
func (e *DecodeError) Unwrap() error {
	return e.Err
}

func newAPIError(statusCode int, payload []byte) *APIError {
	apiError := &APIError{
		StatusCode: statusCode,
		Message:    http.StatusText(statusCode),
		Retryable:  statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests || statusCode >= 500,
	}
	var envelope chatErrorEnvelope
	if err := json.Unmarshal(payload, &envelope); err == nil && envelope.Error.Message != "" {
		apiError.Message = envelope.Error.Message
		if envelope.Error.Code != nil {
			apiError.Code = fmt.Sprintf("%v", envelope.Error.Code)
		}
	}
	return apiError
}
