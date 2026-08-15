package golem_test

import (
	"context"
	"errors"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
)

type fakeModel struct {
	request  model.Request
	response model.Response
	err      error
}

func (m *fakeModel) Generate(_ context.Context, request model.Request) (model.Response, error) {
	m.request = request
	return m.response, m.err
}

func TestAgentRunBuildsRequestAndPreservesEvidence(t *testing.T) {
	t.Parallel()

	client := &fakeModel{response: model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "42"},
		Usage:   model.Usage{InputTokens: 12, OutputTokens: 1},
	}}
	agent, err := golem.New[struct{ Tenant string }, int](
		client,
		golem.DecodeFunc[int](func(_ context.Context, response model.Response) (int, error) {
			if response.Message.Content != "42" {
				t.Fatalf("decoder received %q", response.Message.Content)
			}
			return 42, nil
		}),
		golem.WithInstructions[struct{ Tenant string }, int]("Be concise."),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{ Tenant string }]{Deps: struct{ Tenant string }{"acme"}}, "What is the answer?")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != 42 {
		t.Fatalf("Output = %d, want 42", result.Output)
	}
	if result.Usage.InputTokens != 12 || len(result.Messages) != 3 {
		t.Fatalf("result evidence = %#v", result)
	}
	if got, want := client.request.Messages, []model.Message{
		{Role: model.RoleSystem, Content: "Be concise."},
		{Role: model.RoleUser, Content: "What is the answer?"},
	}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("request messages = %#v, want %#v", got, want)
	}
}

func TestAgentRunClassifiesModelAndDecodeFailures(t *testing.T) {
	t.Parallel()

	modelFailure := errors.New("provider unavailable")
	decodeFailure := errors.New("invalid response")
	tests := []struct {
		name   string
		model  *fakeModel
		decode golem.DecodeFunc[string]
		stage  golem.Stage
		cause  error
	}{
		{
			name:   "model",
			model:  &fakeModel{err: modelFailure},
			decode: func(context.Context, model.Response) (string, error) { return "", nil },
			stage:  golem.StageModel,
			cause:  modelFailure,
		},
		{
			name:   "decode",
			model:  &fakeModel{response: model.Response{}},
			decode: func(context.Context, model.Response) (string, error) { return "", decodeFailure },
			stage:  golem.StageDecode,
			cause:  decodeFailure,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			agent, err := golem.New[struct{}, string](test.model, test.decode)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hello")
			var runErr *golem.RunError
			if !errors.As(err, &runErr) || runErr.Stage != test.stage {
				t.Fatalf("Run() error = %v, want stage %q", err, test.stage)
			}
			if !errors.Is(err, test.cause) {
				t.Fatalf("Run() error = %v, want cause %v", err, test.cause)
			}
		})
	}
}
