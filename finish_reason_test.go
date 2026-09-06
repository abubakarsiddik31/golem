package golem_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// A run preserves the provider's terminal cause of its final model turn:
// on the result when it succeeds or pauses, and on RunError.Partial when
// a completed turn is what made the run fail — a truncated turn that the
// decoder rejects, most commonly. These tests pin that surface.

func TestResultCarriesFinishReason(t *testing.T) {
	t.Parallel()

	client := &fakeModel{response: model.Response{
		Message:      model.Message{Role: model.RoleAssistant, Content: "42"},
		Usage:        model.Usage{InputTokens: 12, OutputTokens: 3},
		FinishReason: model.FinishStop,
	}}
	agent, err := golem.New[struct{}, string](client, echoDecoder())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "answer")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.FinishReason != model.FinishStop {
		t.Fatalf("FinishReason = %q, want stop", result.FinishReason)
	}
}

func TestResultFinishReasonFollowsTheFinalTurn(t *testing.T) {
	t.Parallel()

	// The first turn is truncated and rejected; the correction round
	// answers fully. The result carries the final turn's cause.
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "{oops"}, FinishReason: model.FinishLength},
		{Message: model.Message{Role: model.RoleAssistant, Content: "yes"}, FinishReason: model.FinishStop},
	}}
	agent, err := golem.New[struct{}, string](client, strictDecoder("yes"),
		golem.WithOutputRetries[struct{}, string](1),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "answer")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.FinishReason != model.FinishStop {
		t.Fatalf("FinishReason = %q, want the final turn's stop", result.FinishReason)
	}
}

func TestPausedResultCarriesFinishReason(t *testing.T) {
	t.Parallel()

	deferredTool := tool.MustNew(tool.Tool[gateDeps]{
		Name:        "delete_file",
		Description: "Delete a file.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps gateDeps, args json.RawMessage) (string, error) {
			return "", &tool.Deferred{Kind: tool.DeferApproval, Reason: "sign-off"}
		},
	})
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "delete_file", Args: json.RawMessage(`{"path":"a"}`)},
		}}, FinishReason: model.FinishToolCall},
	}}
	agent, err := golem.New[gateDeps, string](client, echoDecoder(),
		golem.WithTools[gateDeps, string](deferredTool),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[gateDeps]{}, "clean up")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Pending == nil {
		t.Fatalf("Pending is nil, want the deferred call")
	}
	if result.FinishReason != model.FinishToolCall {
		t.Fatalf("FinishReason = %q, want the requesting turn's tool_call", result.FinishReason)
	}
}

func TestDecodeFailurePartialKeepsFinishReason(t *testing.T) {
	t.Parallel()

	// The provider truncated the response mid-JSON: the cause that made
	// the output undecodable must survive on the partial evidence.
	client := &fakeModel{response: model.Response{
		Message:      model.Message{Role: model.RoleAssistant, Content: `{"value": "trunc`},
		FinishReason: model.FinishLength,
	}}
	agent, err := golem.New[struct{}, string](client, strictDecoder("anything"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "answer")
	if err == nil {
		t.Fatalf("Run() error = nil, want a decode failure")
	}
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageDecode {
		t.Fatalf("error = %v, want the decode stage", err)
	}
	if runErr.Partial == nil || runErr.Partial.FinishReason != model.FinishLength {
		t.Fatalf("Partial = %#v, want FinishReason length", runErr.Partial)
	}
}

func TestStreamResultCarriesFinishReason(t *testing.T) {
	t.Parallel()

	client := &streamQueuedModel{queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "six"}, FinishReason: model.FinishStop},
	}}}
	agent, err := golem.New[struct{}, string](client, echoDecoder())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.RunStream(context.Background(), golem.RunContext[struct{}]{}, "roll", nil)
	if err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}
	if result.FinishReason != model.FinishStop {
		t.Fatalf("FinishReason = %q, want stop: streams preserve the cause", result.FinishReason)
	}
}
