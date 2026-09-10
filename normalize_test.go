package golem_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
)

// truncatedHistory is a crashed run's evidence: call-1 executed, call-2
// never got a result (the stream also died mid-arguments), and one
// orphaned result references a call that is absent.
func truncatedHistory() []model.Message {
	return []model.Message{
		{Role: model.RoleUser, Content: "roll twice"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "roll", Args: json.RawMessage(`{"sides":6}`)},
			{ID: "call-2", Name: "roll", Args: json.RawMessage(`{"sides":`)},
		}},
		{Role: model.RoleTool, ToolCallID: "call-1", ToolName: "roll", Content: "4"},
		{Role: model.RoleTool, ToolCallID: "ghost", ToolName: "roll", Content: "orphaned"},
	}
}

func TestNormalizeHistoryReportsEveryChange(t *testing.T) {
	t.Parallel()

	history := truncatedHistory()
	normalized, report := golem.NormalizeHistory(history)

	if !reflect.DeepEqual(report.Synthesized, []string{"call-2"}) {
		t.Fatalf("Synthesized = %#v, want call-2", report.Synthesized)
	}
	if !reflect.DeepEqual(report.Dropped, []string{"ghost"}) {
		t.Fatalf("Dropped = %#v, want ghost", report.Dropped)
	}
	if !reflect.DeepEqual(report.Truncated, []string{"call-2"}) {
		t.Fatalf("Truncated = %#v, want call-2", report.Truncated)
	}

	// call-2 received the synthesized interrupted result, placed directly
	// after the assistant turn; the orphaned result is gone; the
	// truncated arguments stay verbatim.
	var answered bool
	var call2 model.Message
	for _, message := range normalized {
		if message.Role == model.RoleTool && message.ToolCallID == "call-2" {
			answered = true
			call2 = message
		}
	}
	if !answered || call2.Content == "" {
		t.Fatalf("normalized history = %#v, want a synthesized result for call-2", normalized)
	}
	if string(normalized[1].ToolCalls[1].Args) != `{"sides":` {
		t.Fatalf("truncated args rewritten: %s", normalized[1].ToolCalls[1].Args)
	}
	if len(normalized) != len(history) {
		t.Fatalf("normalized length = %d, want one-for-one (synthesis replaces the dropped slot)", len(normalized))
	}
}

func TestNormalizeHistoryIsIdempotent(t *testing.T) {
	t.Parallel()

	normalized, _ := golem.NormalizeHistory(truncatedHistory())
	again, report := golem.NormalizeHistory(normalized)
	// Pairing repairs were changes, and the second pass makes none.
	if len(report.Synthesized)+len(report.Dropped) != 0 {
		t.Fatalf("second pass report = %+v, want no changes", report)
	}
	// Truncated arguments stay truncated forever: detection reports, it
	// does not repair bytes.
	if !reflect.DeepEqual(report.Truncated, []string{"call-2"}) {
		t.Fatalf("truncated = %#v, want call-2 still detected", report.Truncated)
	}
	if len(again) != len(normalized) {
		t.Fatalf("second pass changed the history length: %d vs %d", len(again), len(normalized))
	}
}

func TestNormalizeHistoryLeavesCleanHistoryAlone(t *testing.T) {
	t.Parallel()

	history := []model.Message{
		{Role: model.RoleUser, Content: "hi"},
		{Role: model.RoleAssistant, Content: "hello", ToolCalls: nil},
	}
	normalized, report := golem.NormalizeHistory(history)
	if len(report.Synthesized)+len(report.Dropped)+len(report.Truncated) != 0 {
		t.Fatalf("report = %+v, want empty", report)
	}
	if len(normalized) != 2 {
		t.Fatalf("normalized = %#v", normalized)
	}
}

func TestRunWithHistoryAcceptsTruncatedArguments(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "picked up where it left off"}},
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{},
		truncatedHistory(), "go on")
	if err != nil {
		t.Fatalf("RunWithHistory() error = %v, want the damaged history to run", err)
	}
}
