package golem_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// rollTool rejects non-positive n as correctable model content.
func rollTool(t *testing.T) tool.Tool[struct{}] {
	t.Helper()
	return tool.MustNew(tool.Tool[struct{}]{
		Name:        "roll",
		Description: "Roll a die; n must be positive.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer"}}}`),
		Exec: func(_ context.Context, _ struct{}, args json.RawMessage) (string, error) {
			var input struct {
				N int `json:"n"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return "", err
			}
			if input.N <= 0 {
				return "", &model.ModelRetry{Err: fmt.Errorf("n must be positive, got %d", input.N)}
			}
			return fmt.Sprintf("rolled %d", input.N), nil
		},
	})
}

func rollCall(id, args string) model.Response {
	return model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
		{ID: id, Name: "roll", Args: json.RawMessage(args)},
	}}}
}

func TestAgentRunCorrectsRejectedToolCalls(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		rollCall("call-1", `{"n":0}`),
		rollCall("call-2", `{"n":4}`),
		{Message: model.Message{Role: model.RoleAssistant, Content: "settled"}, Usage: model.Usage{InputTokens: 6, OutputTokens: 3}},
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](rollTool(t)),
		golem.WithToolRetries[struct{}, string](1))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "roll a 4")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "settled" {
		t.Fatalf("Output = %q", result.Output)
	}
	if result.Usage != (model.Usage{InputTokens: 6, OutputTokens: 3}) {
		t.Fatalf("Usage = %#v", result.Usage)
	}

	// The second model request must end with the rejection delivered as
	// the rejected call's tool result.
	if len(client.requests) != 3 {
		t.Fatalf("model turns = %d, want 3", len(client.requests))
	}
	second := client.requests[1].Messages
	rejection := second[len(second)-1]
	if rejection.Role != model.RoleTool || rejection.ToolCallID != "call-1" || rejection.ToolName != "roll" {
		t.Fatalf("rejection message = %#v", rejection)
	}
	if !strings.Contains(rejection.Content, "Your tool call was rejected") ||
		!strings.Contains(rejection.Content, "n must be positive, got 0") {
		t.Fatalf("rejection content = %q", rejection.Content)
	}

	// Evidence keeps the rejected call and both results, in order:
	// user, assistant-call, tool(rejection), assistant-call, tool(result),
	// assistant-final.
	wantRoles := []model.Role{model.RoleUser, model.RoleAssistant, model.RoleTool, model.RoleAssistant, model.RoleTool, model.RoleAssistant}
	if len(result.Messages) != len(wantRoles) {
		t.Fatalf("evidence = %#v", result.Messages)
	}
	for i, role := range wantRoles {
		if result.Messages[i].Role != role {
			t.Fatalf("evidence[%d].Role = %q, want %q", i, result.Messages[i].Role, role)
		}
	}
}

func TestAgentRunAbortsToolRejectionsWithoutBudget(t *testing.T) {
	t.Parallel()

	reason := "n must be positive, got 0"
	client := &queuedModel{responses: []model.Response{
		rollCall("call-1", `{"n":0}`),
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](rollTool(t)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "roll")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageTool {
		t.Fatalf("Run() error = %v, want tool stage", err)
	}
	var rejection *model.ModelRetry
	if !errors.As(err, &rejection) {
		t.Fatalf("Run() error = %v, want ModelRetry in the chain", err)
	}
	if rejection.Err == nil || rejection.Err.Error() != reason {
		t.Fatalf("rejection cause = %v, want the tool's reason", rejection.Err)
	}
	if strings.Contains(err.Error(), "after") {
		t.Fatalf("Run() error = %q, default rejection must not carry an attempt count", err.Error())
	}
	if len(client.requests) != 1 {
		t.Fatalf("model turns = %d, want 1", len(client.requests))
	}
}

func TestNewRejectsNegativeToolRetries(t *testing.T) {
	t.Parallel()

	if _, err := golem.New[struct{}, string](&queuedModel{}, decoderOf(),
		golem.WithToolRetries[struct{}, string](-1)); err == nil {
		t.Fatal("New() error = nil, want negative tool retries rejection")
	}
}

func TestAgentRunUsesPerToolRetryLimit(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		rollCall("call-1", `{"n":0}`),
		rollCall("call-2", `{"n":0}`),
	}}
	limit := 1
	roll := rollTool(t)
	roll.MaxRetries = &limit
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](roll),
		golem.WithToolRetries[struct{}, string](5))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "roll")
	if err == nil || !strings.Contains(err.Error(), "after 2 attempts") {
		t.Fatalf("Run() error = %v, want the per-tool exhaustion", err)
	}
	if len(client.requests) != 2 {
		t.Fatalf("model turns = %d, want 2", len(client.requests))
	}
}
