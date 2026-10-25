package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/abubakarsiddik31/golem/internal/runner"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// benchUsage keeps usage accounting inside the measured path without
// approaching any limit.
var benchUsage = model.Usage{InputTokens: 120, OutputTokens: 40}

// benchTools is the one-tool inventory every execute benchmark runs
// against: enough to exercise spec validation and dispatch without
// dominating the measurement.
func benchTools(b *testing.B) []tool.Tool[deps] {
	b.Helper()
	return []tool.Tool[deps]{echoTool(b)}
}

// BenchmarkExecuteSingleTurn measures one complete run that ends on the
// first model turn: request copying, history normalization, the turn
// event sequence, and outcome assembly, with no tool dispatch.
func BenchmarkExecuteSingleTurn(b *testing.B) {
	tools := benchTools(b)
	request := model.Request{
		Messages:  []model.Message{{Role: model.RoleUser, Content: "hello"}},
		ToolSpecs: specsFor(b, tools...),
	}
	ctx := context.Background()

	b.ReportAllocs()
	for b.Loop() {
		m := &scriptedModel{responses: []model.Response{textResponse("done", benchUsage)}}
		if _, err := runner.Execute(ctx, m, tools, deps{Tenant: "acme"}, request, 10, noRetries, 0, ""); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkExecuteToolRound measures a run that dispatches one tool call
// and then ends: the model turn, tool execution, result message
// construction, and the second model turn.
func BenchmarkExecuteToolRound(b *testing.B) {
	tools := benchTools(b)
	request := model.Request{
		Messages:  []model.Message{{Role: model.RoleUser, Content: "roll"}},
		ToolSpecs: specsFor(b, tools...),
	}
	ctx := context.Background()

	b.ReportAllocs()
	for b.Loop() {
		m := &scriptedModel{responses: []model.Response{
			toolResponse(model.ToolCall{ID: "call-1", Name: "echo", Args: json.RawMessage(`{"n":1}`)}),
			textResponse("done", benchUsage),
		}}
		if _, err := runner.Execute(ctx, m, tools, deps{Tenant: "acme"}, request, 10, noRetries, 0, ""); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkExecuteLongHistory measures per-turn normalization over a
// 201-message conversation, the shape a long-lived agent accumulates.
// Cost that grows with history length shows up here and not in the
// single-turn benchmark.
func BenchmarkExecuteLongHistory(b *testing.B) {
	tools := benchTools(b)
	messages := make([]model.Message, 0, 201)
	messages = append(messages, model.Message{Role: model.RoleUser, Content: "hello"})
	for i := 0; i < 100; i++ {
		messages = append(messages,
			model.Message{Role: model.RoleAssistant, Content: fmt.Sprintf("reply %d", i)},
			model.Message{Role: model.RoleUser, Content: fmt.Sprintf("follow-up %d", i)},
		)
	}
	request := model.Request{Messages: messages, ToolSpecs: specsFor(b, tools...)}
	ctx := context.Background()

	b.ReportAllocs()
	for b.Loop() {
		m := &scriptedModel{responses: []model.Response{textResponse("done", benchUsage)}}
		if _, err := runner.Execute(ctx, m, tools, deps{Tenant: "acme"}, request, 10, noRetries, 0, ""); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRepairHistory measures one normalization pass over a damaged
// history: two unanswered tool calls that need synthesized results and
// one orphaned result that must be dropped. Every resumed run pays this.
func BenchmarkRepairHistory(b *testing.B) {
	history := []model.Message{
		{Role: model.RoleUser, Content: "run it"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "echo", Args: json.RawMessage(`{"n":1}`)},
			{ID: "call-2", Name: "echo", Args: json.RawMessage(`{"n":2}`)},
		}},
		{Role: model.RoleTool, ToolCallID: "orphan", ToolName: "echo", Content: "unpaired"},
	}

	b.ReportAllocs()
	for b.Loop() {
		if got := runner.RepairHistory(history); len(got) == 0 {
			b.Fatal("RepairHistory returned an empty history")
		}
	}
}

// BenchmarkHistoryToJSON measures the durable-history encoding an
// application pays when it stores a conversation after a run.
func BenchmarkHistoryToJSON(b *testing.B) {
	history := make([]model.Message, 0, 101)
	history = append(history, model.Message{Role: model.RoleUser, Content: "hello"})
	for i := 0; i < 50; i++ {
		history = append(history,
			model.Message{Role: model.RoleAssistant, Content: fmt.Sprintf("reply %d", i)},
			model.Message{Role: model.RoleUser, Content: fmt.Sprintf("follow-up %d", i)},
		)
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(history); err != nil {
			b.Fatal(err)
		}
	}
}
