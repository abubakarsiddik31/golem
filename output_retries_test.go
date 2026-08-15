package golem_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
)

// strictDecoder accepts only the exact want content; anything else is a
// ModelRetry rejection the model can correct.
func strictDecoder(want string) golem.DecodeFunc[string] {
	return func(_ context.Context, r model.Response) (string, error) {
		if r.Message.Content != want {
			return "", &model.ModelRetry{Err: errors.New("answer must be exactly " + want)}
		}
		return r.Message.Content, nil
	}
}

func TestAgentRunCorrectsRejectedOutput(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "almost"}, Usage: model.Usage{InputTokens: 3, OutputTokens: 2}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "exact"}, Usage: model.Usage{InputTokens: 5, OutputTokens: 1}},
	}}
	agent, err := golem.New[struct{}, string](client, strictDecoder("exact"),
		golem.WithOutputRetries[struct{}, string](1))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "answer exactly")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "exact" {
		t.Fatalf("Output = %q", result.Output)
	}

	// The correction round asked the model again, with the rejection as the
	// last message of the second request.
	if len(client.requests) != 2 {
		t.Fatalf("model rounds = %d, want 2", len(client.requests))
	}
	second := client.requests[1].Messages
	if len(second) != 3 || second[len(second)-1].Role != model.RoleUser ||
		!strings.Contains(second[len(second)-1].Content, "answer must be exactly exact") {
		t.Fatalf("second request = %#v, want rejection prompt last", second)
	}

	// Evidence keeps the rejected response and the rejection prompt; usage
	// sums across rounds.
	wantRoles := []model.Role{model.RoleUser, model.RoleAssistant, model.RoleUser, model.RoleAssistant}
	if len(result.Messages) != len(wantRoles) {
		t.Fatalf("evidence = %#v", result.Messages)
	}
	for i, role := range wantRoles {
		if result.Messages[i].Role != role {
			t.Fatalf("evidence[%d].Role = %q, want %q", i, result.Messages[i].Role, role)
		}
	}
	if result.Messages[1].Content != "almost" {
		t.Fatalf("rejected response missing from evidence: %#v", result.Messages)
	}
	if result.Usage != (model.Usage{InputTokens: 8, OutputTokens: 3}) {
		t.Fatalf("usage = %#v, want summed across rounds", result.Usage)
	}
}

func TestAgentRunFailsNonRetryableDecodeErrorsImmediately(t *testing.T) {
	t.Parallel()

	cause := errors.New("cannot become an int")
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "nope"}},
	}}
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return "", cause
		}),
		golem.WithOutputRetries[struct{}, string](3))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "answer")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageDecode {
		t.Fatalf("Run() error = %v, want decode stage", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("Run() error = %v, want cause %v", err, cause)
	}
	if len(client.requests) != 1 {
		t.Fatalf("model rounds = %d, want 1", len(client.requests))
	}
}

func TestAgentRunWrapsCauseAfterExhaustedOutputRetries(t *testing.T) {
	t.Parallel()

	reason := errors.New("answer must be exactly exact")
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "wrong 1"}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "wrong 2"}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "wrong 3"}},
	}}
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return "", &model.ModelRetry{Err: reason}
		}),
		golem.WithOutputRetries[struct{}, string](2))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "answer")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageDecode {
		t.Fatalf("Run() error = %v, want decode stage", err)
	}
	if !errors.Is(err, reason) {
		t.Fatalf("Run() error = %v, want rejection reason %v", err, reason)
	}
	var rejection *model.ModelRetry
	if !errors.As(err, &rejection) {
		t.Fatalf("Run() error = %v, want ModelRetry in the chain", err)
	}
	if !strings.Contains(err.Error(), "after 3 attempts") {
		t.Fatalf("Run() error = %q, want attempt count in message", err.Error())
	}
	if len(client.requests) != 3 {
		t.Fatalf("model rounds = %d, want 3", len(client.requests))
	}
}

func TestAgentRunRejectsOutputOnceByDefault(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "almost"}},
	}}
	agent, err := golem.New[struct{}, string](client, strictDecoder("exact"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "answer exactly")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageDecode {
		t.Fatalf("Run() error = %v, want decode stage", err)
	}
	if strings.Contains(err.Error(), "after") {
		t.Fatalf("Run() error = %q, default rejection must not carry an attempt count", err.Error())
	}
	if len(client.requests) != 1 {
		t.Fatalf("model rounds = %d, want 1", len(client.requests))
	}
}

func TestNewRejectsNegativeOutputRetries(t *testing.T) {
	t.Parallel()

	if _, err := golem.New[struct{}, string](&queuedModel{}, decoderOf(),
		golem.WithOutputRetries[struct{}, string](-1)); err == nil {
		t.Fatal("New() error = nil, want negative output retries rejection")
	}
}
