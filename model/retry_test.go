package model_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

// classifiedError is the smallest adapter-style error carrying a retryable
// classification.
type classifiedError struct {
	message   string
	retryable bool
}

func (e *classifiedError) Error() string { return e.message }
func (e *classifiedError) Retryable() bool {
	return e.retryable
}

func TestIsRetryable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"retryable classification", &classifiedError{message: "429", retryable: true}, true},
		{"non-retryable classification", &classifiedError{message: "401"}, false},
		{"retryable inside a wrapped chain",
			fmt.Errorf("generate failed: %w", &classifiedError{message: "500", retryable: true}), true},
		{"non-retryable inside a wrapped chain",
			fmt.Errorf("generate failed: %w", &classifiedError{message: "400"}), false},
		{"unclassified error", errors.New("plain failure"), false},
		{"canceled context", context.Canceled, false},
		{"deadline exceeded", context.DeadlineExceeded, false},
		{
			"cancellation and retryable classification in one chain",
			fmt.Errorf("after %w: %w", context.Canceled, &classifiedError{message: "429", retryable: true}),
			false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := model.IsRetryable(test.err); got != test.want {
				t.Fatalf("IsRetryable(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

func TestIsRetryableOfNilError(t *testing.T) {
	t.Parallel()
	if model.IsRetryable(nil) {
		t.Fatal("IsRetryable(nil) = true, want false")
	}
}

func TestModelRetrySignalsCorrection(t *testing.T) {
	t.Parallel()

	reason := errors.New("guess must be between 1 and 6")
	rejection := &model.ModelRetry{Err: reason}

	if !errors.Is(rejection, reason) {
		t.Fatal("ModelRetry must expose its reason through errors.Is")
	}
	wrapped := fmt.Errorf("decode: %w", rejection)
	var retry *model.ModelRetry
	if !errors.As(wrapped, &retry) {
		t.Fatal("ModelRetry must be reachable through errors.As in a wrapped chain")
	}
	if !errors.Is(wrapped, reason) {
		t.Fatal("wrapped ModelRetry must expose its reason")
	}
}

func TestModelRetryIsNotTransportRetryable(t *testing.T) {
	t.Parallel()

	if model.IsRetryable(&model.ModelRetry{Err: errors.New("invalid output")}) {
		t.Fatal("ModelRetry must not classify as transport-retryable")
	}
}
