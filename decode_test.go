package golem_test

import (
	"context"
	"errors"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
)

type cityOutput struct {
	Name    string `json:"name"`
	Country string `json:"country"`
}

func TestDecodeJSONDecodesTypedOutput(t *testing.T) {
	t.Parallel()

	client := &fakeModel{response: model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: `{"name":"Lagos","country":"Nigeria"}`},
	}}
	agent, err := golem.New[struct{}, cityOutput](client, golem.DecodeJSON[cityOutput]())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "name a city")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != (cityOutput{Name: "Lagos", Country: "Nigeria"}) {
		t.Fatalf("Output = %#v", result.Output)
	}
}

func TestDecodeJSONRejectsMalformedContent(t *testing.T) {
	t.Parallel()

	_, err := golem.DecodeJSON[cityOutput]().Decode(context.Background(), model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "Lagos"},
	})
	var rejection *model.ModelRetry
	if !errors.As(err, &rejection) {
		t.Fatalf("error = %v, want a *model.ModelRetry rejection", err)
	}
}

func TestAgentRunCorrectsMalformedJSONOutput(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "Lagos"}},
		{Message: model.Message{Role: model.RoleAssistant, Content: `{"name":"Lagos","country":"Nigeria"}`}},
	}}
	agent, err := golem.New[struct{}, cityOutput](client, golem.DecodeJSON[cityOutput](),
		golem.WithOutputRetries[struct{}, cityOutput](1),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "name a city")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != (cityOutput{Name: "Lagos", Country: "Nigeria"}) {
		t.Fatalf("Output = %#v", result.Output)
	}
	if len(client.requests) != 2 {
		t.Fatalf("model rounds = %d, want 2", len(client.requests))
	}
	// The rejected response stays in the evidence.
	if len(result.Messages) != 4 || result.Messages[1].Content != "Lagos" {
		t.Fatalf("evidence = %#v", result.Messages)
	}
}

func TestAgentRunFailsDecodeStageForMalformedJSONWithoutBudget(t *testing.T) {
	t.Parallel()

	agent, err := golem.New[struct{}, cityOutput](
		&fakeModel{response: model.Response{
			Message: model.Message{Role: model.RoleAssistant, Content: "Lagos"},
		}},
		golem.DecodeJSON[cityOutput](),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "name a city")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageDecode {
		t.Fatalf("error = %v, want decode stage", err)
	}
	var rejection *model.ModelRetry
	if !errors.As(err, &rejection) {
		t.Fatalf("error = %v, want the ModelRetry cause preserved", err)
	}
}
