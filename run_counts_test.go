package golem_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

// A successful Result carries the same activity counts the usage limit
// enforces and RunError.Partial preserves on failure: one request per
// provider call, retried attempts included, and one tool call per
// executed tool. These tests pin that success-path surface.

func TestResultCarriesActivityCounts(t *testing.T) {
	t.Parallel()

	client, tool := countingTool(t)
	agent, err := golem.New[struct{}, string](client, echoDecoder(),
		golem.WithTools[struct{}, string](tool),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "run it")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Requests != 2 {
		t.Fatalf("Requests = %d, want 2: the tool turn and the answer turn", result.Requests)
	}
	if result.ToolCalls != 1 {
		t.Fatalf("ToolCalls = %d, want 1", result.ToolCalls)
	}
	if result.Usage != (model.Usage{InputTokens: 7, OutputTokens: 3}) {
		t.Fatalf("Usage = %#v, want both turns summed", result.Usage)
	}
}

func TestResultCountsRetriedModelCalls(t *testing.T) {
	t.Parallel()

	client := &flakyModel{failCalls: 1, failure: &transientError{message: "transient"},
		response: model.Response{
			Message: model.Message{Role: model.RoleAssistant, Content: "42"},
			Usage:   model.Usage{InputTokens: 4, OutputTokens: 1},
		}}
	agent, err := golem.New[struct{}, string](client, echoDecoder(),
		golem.WithMaxAttempts[struct{}, string](2),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "answer")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Requests != 2 {
		t.Fatalf("Requests = %d, want 2: the failed attempt counts", result.Requests)
	}
	if result.ToolCalls != 0 {
		t.Fatalf("ToolCalls = %d, want 0", result.ToolCalls)
	}
}

func TestResultCountsCorrectionRounds(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "no"}, Usage: model.Usage{InputTokens: 3, OutputTokens: 2}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "yes"}, Usage: model.Usage{InputTokens: 3, OutputTokens: 2}},
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
	if result.Requests != 2 {
		t.Fatalf("Requests = %d, want 2: the corrected round counts", result.Requests)
	}
	if result.ToolCalls != 0 {
		t.Fatalf("ToolCalls = %d, want 0", result.ToolCalls)
	}
}

func TestPausedResultCarriesActivityCounts(t *testing.T) {
	t.Parallel()

	rec := &gateRecorder{}
	deleteFile := tool.MustNew(tool.Tool[gateDeps]{
		Name:        "delete_file",
		Description: "Delete a file.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps gateDeps, args json.RawMessage) (string, error) {
			rec.runs++
			return "", &tool.Deferred{Kind: tool.DeferApproval, Reason: "sign-off"}
		},
	})
	stat := tool.MustNew(tool.Tool[gateDeps]{
		Name:        "stat_file",
		Description: "Stat a file.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps gateDeps, args json.RawMessage) (string, error) {
			return "size=12", nil
		},
	})
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "delete_file", Args: json.RawMessage(`{"path":"a"}`)},
			{ID: "call-2", Name: "stat_file", Args: json.RawMessage(`{}`)},
		}}, Usage: model.Usage{InputTokens: 6, OutputTokens: 2}},
	}}
	agent, err := golem.New[gateDeps, string](client, echoDecoder(),
		golem.WithTools[gateDeps, string](deleteFile, stat),
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
	// The executed stat call counts; the deferred delete call does not.
	if result.Requests != 1 || result.ToolCalls != 1 {
		t.Fatalf("Requests = %d, ToolCalls = %d, want 1 and 1", result.Requests, result.ToolCalls)
	}
}

func TestStreamResultCarriesActivityCounts(t *testing.T) {
	t.Parallel()

	toolCall := model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
		{ID: "call-1", Name: "roll", Args: json.RawMessage(`{}`)},
	}}}
	_, roll := countingTool(t)
	client := &streamQueuedModel{queuedModel{responses: []model.Response{
		toolCall,
		{Message: model.Message{Role: model.RoleAssistant, Content: "six"}, Usage: model.Usage{InputTokens: 5, OutputTokens: 1}},
	}}}
	agent, err := golem.New[struct{}, string](client, echoDecoder(),
		golem.WithTools[struct{}, string](roll),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.RunStream(context.Background(), golem.RunContext[struct{}]{}, "roll", nil)
	if err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}
	if result.Requests != 2 || result.ToolCalls != 1 {
		t.Fatalf("Requests = %d, ToolCalls = %d, want 2 and 1: streams count like plain runs", result.Requests, result.ToolCalls)
	}
}

func TestDelegatedActivityStaysInSubAgentResult(t *testing.T) {
	t.Parallel()

	innerModel := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "inner-1", Name: "lookup", Args: json.RawMessage(`{}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Paris"}},
		// The sequence again for the standalone inner run below.
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "inner-2", Name: "lookup", Args: json.RawMessage(`{}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Paris"}},
	)
	lookup := tool.MustNew(tool.Tool[delegationDeps]{
		Name:        "lookup",
		Description: "Look up a fact.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps delegationDeps, args json.RawMessage) (string, error) {
			return "capital of France: Paris", nil
		},
	})
	inner, err := golem.New[delegationDeps, string](innerModel, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[delegationDeps, string](lookup),
	)
	if err != nil {
		t.Fatalf("inner New() error = %v", err)
	}

	agentTool, err := inner.AsTool("researcher", "Answers geography questions")
	if err != nil {
		t.Fatalf("AsTool() error = %v", err)
	}
	outerModel := testmodel.New().Respond(
		agentCall("c1", `{"prompt":"Capital of France?"}`),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "The capital is Paris."}},
	)
	outer, err := golem.New[delegationDeps, string](outerModel, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[delegationDeps, string](agentTool),
	)
	if err != nil {
		t.Fatalf("outer New() error = %v", err)
	}

	outerResult, err := outer.Run(context.Background(), golem.RunContext[delegationDeps]{}, "I need a capital.")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The parent sees one delegation tool call across its two requests;
	// the sub-agent's own two turns and one execution stay in the
	// sub-agent's run.
	if outerResult.Requests != 2 || outerResult.ToolCalls != 1 {
		t.Fatalf("outer Requests = %d, ToolCalls = %d, want 2 and 1", outerResult.Requests, outerResult.ToolCalls)
	}

	innerResult, err := inner.Run(context.Background(), golem.RunContext[delegationDeps]{}, "Capital of France?")
	if err != nil {
		t.Fatalf("inner Run() error = %v", err)
	}
	if innerResult.Requests != 2 || innerResult.ToolCalls != 1 {
		t.Fatalf("inner Requests = %d, ToolCalls = %d, want 2 and 1", innerResult.Requests, innerResult.ToolCalls)
	}
}

// countingTool builds a model scripted with one tool turn followed by a
// text answer, and the single tool the model calls, reporting one usage
// entry on the tool turn.
func countingTool(t *testing.T) (*queuedModel, tool.Tool[struct{}]) {
	t.Helper()
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "roll", Args: json.RawMessage(`{}`)},
		}}, Usage: model.Usage{InputTokens: 2, OutputTokens: 1}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "six"}, Usage: model.Usage{InputTokens: 5, OutputTokens: 2}},
	}}
	roll := tool.MustNew(tool.Tool[struct{}]{
		Name:        "roll",
		Description: "Roll a die.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (string, error) {
			return "six", nil
		},
	})
	return client, roll
}
