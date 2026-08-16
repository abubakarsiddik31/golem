package golem_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
)

func TestWithOutputSchemaPropagatesToEveryRequest(t *testing.T) {
	t.Parallel()

	schema := json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}`)
	// The first response is rejected so a correction round issues a second
	// request; both must describe the expected output.
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "almost"}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "exact"}},
	}}
	agent, err := golem.New[struct{}, string](client, strictDecoder("exact"),
		golem.WithOutputSchema[struct{}, string](schema),
		golem.WithOutputRetries[struct{}, string](1),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "answer exactly"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(client.requests) != 2 {
		t.Fatalf("model rounds = %d, want 2", len(client.requests))
	}
	for i, request := range client.requests {
		if string(request.OutputSchema) != string(schema) {
			t.Fatalf("request %d output schema = %s, want the configured schema", i, request.OutputSchema)
		}
	}
}

func TestNewValidatesOutputSchema(t *testing.T) {
	t.Parallel()

	decoder := golem.DecodeFunc[string](func(context.Context, model.Response) (string, error) {
		return "", nil
	})
	if _, err := golem.New[struct{}, string](&fakeModel{}, decoder,
		golem.WithOutputSchema[struct{}, string](json.RawMessage(`{"type":`)),
	); err == nil {
		t.Fatal("New() error = nil, want invalid-JSON schema rejection")
	}

	agent, err := golem.New[struct{}, string](&fakeModel{}, decoder,
		golem.WithOutputSchema[struct{}, string](nil),
	)
	if err != nil {
		t.Fatalf("New() with empty schema error = %v", err)
	}
	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hi")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "" {
		t.Fatalf("Output = %q, want empty", result.Output)
	}
}
