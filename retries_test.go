package golem_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
)

// transientError classifies itself retryable, like an adapter's APIError
// for 429 and 5xx responses.
type transientError struct{ message string }

func (e *transientError) Error() string   { return e.message }
func (e *transientError) Retryable() bool { return true }

// flakyModel fails its first failCalls calls with failure, then returns
// response, recording every call.
type flakyModel struct {
	failCalls int
	failure   error
	response  model.Response
	calls     int
}

func (m *flakyModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	m.calls++
	if m.calls <= m.failCalls {
		return model.Response{}, m.failure
	}
	return m.response, nil
}

func retryAgent(t *testing.T, m model.Model, options ...golem.Option[struct{}, string]) *golem.Agent[struct{}, string] {
	t.Helper()
	agent, err := golem.New[struct{}, string](m, decoderOf(), options...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return agent
}

func TestAgentRunRetriesRetryableModelFailures(t *testing.T) {
	t.Parallel()

	m := &flakyModel{
		failCalls: 1,
		failure:   &transientError{message: "429 rate limited"},
		response: model.Response{
			Message: model.Message{Role: model.RoleAssistant, Content: "recovered"},
			Usage:   model.Usage{InputTokens: 9, OutputTokens: 1},
		},
	}
	agent := retryAgent(t, m,
		golem.WithMaxAttempts[struct{}, string](2),
		golem.WithRetryBackoff[struct{}, string](func(int) time.Duration { return 0 }),
	)

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hello")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "recovered" {
		t.Fatalf("Output = %q", result.Output)
	}
	if m.calls != 2 {
		t.Fatalf("model attempts = %d, want 2", m.calls)
	}
}

func TestAgentRunDoesNotRetryNonRetryableModelFailures(t *testing.T) {
	t.Parallel()

	permanent := errors.New("unauthorized")
	m := &flakyModel{failCalls: 100, failure: permanent}
	agent := retryAgent(t, m,
		golem.WithMaxAttempts[struct{}, string](3),
		golem.WithRetryBackoff[struct{}, string](func(int) time.Duration { return 0 }),
	)

	_, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hello")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageModel {
		t.Fatalf("Run() error = %v, want model stage", err)
	}
	if !errors.Is(err, permanent) {
		t.Fatalf("Run() error = %v, want cause %v", err, permanent)
	}
	if m.calls != 1 {
		t.Fatalf("model attempts = %d, want 1", m.calls)
	}
}

func TestAgentRunSurfacesTerminalCauseAfterExhaustedRetries(t *testing.T) {
	t.Parallel()

	overloaded := &transientError{message: "503 overloaded"}
	m := &flakyModel{failCalls: 100, failure: overloaded}
	agent := retryAgent(t, m,
		golem.WithMaxAttempts[struct{}, string](2),
		golem.WithRetryBackoff[struct{}, string](func(int) time.Duration { return 0 }),
	)

	_, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hello")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageModel {
		t.Fatalf("Run() error = %v, want model stage", err)
	}
	if !errors.Is(err, overloaded) {
		t.Fatalf("Run() error = %v, want terminal cause %v", err, overloaded)
	}
	if !strings.Contains(err.Error(), "after 2 attempts") {
		t.Fatalf("Run() error = %q, want attempt count in message", err.Error())
	}
	if m.calls != 2 {
		t.Fatalf("model attempts = %d, want 2", m.calls)
	}
}

func TestNewRejectsInvalidMaxAttempts(t *testing.T) {
	t.Parallel()

	if _, err := golem.New[struct{}, string](&flakyModel{}, decoderOf(),
		golem.WithMaxAttempts[struct{}, string](0)); err == nil {
		t.Fatal("New() error = nil, want max-attempts rejection")
	}
}
