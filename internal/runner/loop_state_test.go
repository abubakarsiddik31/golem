package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestLoopStateRepeatedText(t *testing.T) {
	t.Parallel()

	state := newLoopState(LoopConfig{RepeatedText: 2})
	if err := state.checkText("same answer"); err != nil {
		t.Fatalf("first text flagged: %v", err)
	}
	if err := state.checkText(" Same   Answer "); err != nil {
		t.Fatalf("second text flagged: %v", err)
	}
	err := state.checkText("same answer")
	if err == nil || err.Pattern != "repeated_text" || err.Count != 3 || err.Threshold != 2 {
		t.Fatalf("third text = %#v", err)
	}
	state.reset()
	if err := state.checkText("same answer"); err != nil {
		t.Fatalf("text after reset flagged: %v", err)
	}
	if err := state.checkText("different"); err != nil {
		t.Fatalf("different text flagged: %v", err)
	}
	if err := state.checkText("same answer"); err != nil {
		t.Fatalf("non-consecutive text flagged: %v", err)
	}
}

func TestResultFingerprintDistinguishesOutcomes(t *testing.T) {
	t.Parallel()

	first := resultFingerprint(toolMessage("a", "x", false))
	if first != resultFingerprint(toolMessage("a", "x", false)) {
		t.Fatal("same result produced different fingerprints")
	}
	if first == resultFingerprint(toolMessage("a", "x", true)) {
		t.Fatal("failed flag did not change the fingerprint")
	}
	if first == resultFingerprint(toolMessage("b", "x", false)) {
		t.Fatal("tool name did not change the fingerprint")
	}
}

func toolMessage(toolName, content string, failed bool) model.Message {
	return model.Message{Role: model.RoleTool, ToolName: toolName, Content: content, Failed: failed}
}

func TestLoopStateCallPatterns(t *testing.T) {
	t.Parallel()

	state := newLoopState(LoopConfig{Window: 10, RepeatedCall: 2, Alternating: 2, RepeatedResult: 2})
	calls := []model.ToolCall{
		{Name: "a", Args: []byte(`{"n":1}`)},
		{Name: "a", Args: []byte(`{"n":1}`)},
		{Name: "a", Args: []byte(`{"n":1}`)},
	}
	if err := state.checkPre(calls[:2]); err != nil {
		t.Fatalf("two calls flagged: %v", err)
	}
	if err := state.checkPre(calls); err == nil || err.Pattern != "repeated_call" {
		t.Fatalf("repeated call = %#v", err)
	}

	alternating := []model.ToolCall{
		{Name: "a", Args: []byte(`{"n":1}`)},
		{Name: "b", Args: []byte(`{"n":2}`)},
		{Name: "a", Args: []byte(`{"n":1}`)},
		{Name: "b", Args: []byte(`{"n":2}`)},
	}
	if err := state.checkPre(alternating); err == nil || err.Pattern != "alternating_calls" {
		t.Fatalf("alternating call = %#v", err)
	}

	state.reset()
	state.commitCalls(alternating[:1])
	state.commitResults(alternating[:1], []model.Message{toolMessage("a", "same", false)})
	state.commitCalls(alternating[1:2])
	err := state.commitResults(alternating[1:2], []model.Message{toolMessage("b", "same", false)})
	if err != nil {
		t.Fatalf("distinct call/result pair flagged: %v", err)
	}
}

func TestLoopStateTrimAndNudges(t *testing.T) {
	t.Parallel()

	state := newLoopState(LoopConfig{Window: 1, RepeatedCall: 2})
	state.commitCalls([]model.ToolCall{{Name: "a"}, {Name: "b"}})
	if len(state.calls) != 1 {
		t.Fatalf("window not applied: %#v", state.calls)
	}
	err := &LoopError{Pattern: "repeated_call", ToolName: "weird tool", ToolNames: []string{"weird tool"}, Count: 3, Threshold: 2, Message: "stop"}
	if nudge := nudgeMessage(err); nudge.Role != model.RoleUser || nudge.Content != "stop" {
		t.Fatalf("nudge = %#v", nudge)
	}
	results := nudgeToolResults([]model.ToolCall{{ID: "1", Name: "a"}}, "blocked")
	if len(results) != 1 || !results[0].Failed || results[0].Content != "blocked" {
		t.Fatalf("nudge results = %#v", results)
	}
	if got := safeToolName("a b/c"); got != "a_b_c" {
		t.Fatalf("safeToolName = %q", got)
	}
}

func TestApplyOutputPolicySpillMarker(t *testing.T) {
	t.Parallel()

	text := "header\n" + string(make([]byte, 500)) + "tail"
	config := ToolConfig{Spill: &SpillConfig{Threshold: 10, Put: func(context.Context, string) (string, error) {
		return "handle-1", nil
	}}}
	got := applyOutputPolicy(context.Background(), "read_log", 0, text, config)
	if !strings.Contains(got, `handle "handle-1"`) || !strings.Contains(got, "read_tool_result") {
		t.Fatalf("spill marker = %q", got)
	}
	if previewText("hello", 10) != "hello" {
		t.Fatal("short preview changed")
	}
	if len(previewText("hello", 3)) != 3 {
		t.Fatal("preview not bounded")
	}
}
