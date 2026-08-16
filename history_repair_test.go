package golem_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
)

func danglingHistory() []model.Message {
	return []model.Message{
		{Role: model.RoleUser, Content: "Who is the player?"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "get_player_name", Args: json.RawMessage(`{}`)},
		}},
	}
}

func TestRunWithHistoryRepairsDanglingToolCalls(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "Anne is the player"}},
	}}
	agent, err := golem.New[playerDeps, string](client, decoderOf(),
		golem.WithTools[playerDeps, string](toolForHistory(t)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.RunWithHistory(context.Background(),
		golem.RunContext[playerDeps]{Deps: playerDeps{Name: "Anne"}},
		danglingHistory(), "Try again")
	if err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}

	// The dangling call is closed out before the new prompt: user,
	// assistant-call, synthesized tool result, user prompt.
	request := client.requests[0]
	wantRoles := []model.Role{model.RoleUser, model.RoleAssistant, model.RoleTool, model.RoleUser}
	if len(request.Messages) != len(wantRoles) {
		t.Fatalf("request messages = %#v", request.Messages)
	}
	for i, role := range wantRoles {
		if request.Messages[i].Role != role {
			t.Fatalf("request messages[%d].Role = %q, want %q", i, request.Messages[i].Role, role)
		}
	}
	synthesized := request.Messages[2]
	if synthesized.ToolCallID != "call-1" || synthesized.ToolName != "get_player_name" {
		t.Fatalf("synthesized result = %#v", synthesized)
	}
	if !strings.Contains(synthesized.Content, "interrupted before execution") {
		t.Fatalf("synthesized content = %q, want the interrupted-outcome notice", synthesized.Content)
	}
	if request.Messages[3].Content != "Try again" {
		t.Fatalf("new prompt = %q", request.Messages[3].Content)
	}

	// The repaired pairing becomes part of the run's canonical evidence.
	if len(result.Messages) != 5 || result.Messages[2].Role != model.RoleTool {
		t.Fatalf("evidence = %#v", result.Messages)
	}
	if result.Messages[2].Content != synthesized.Content {
		t.Fatalf("evidence result = %#v, want the synthesized message preserved", result.Messages[2])
	}
}

func TestRunWithHistoryRepairIsIdempotent(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "Anne is the player"}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "Still Anne"}},
	}}
	agent, err := golem.New[playerDeps, string](client, decoderOf(),
		golem.WithTools[playerDeps, string](toolForHistory(t)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	runCtx := golem.RunContext[playerDeps]{Deps: playerDeps{Name: "Anne"}}

	first, err := agent.RunWithHistory(context.Background(), runCtx, danglingHistory(), "Try again")
	if err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}
	_, err = agent.RunWithHistory(context.Background(), runCtx, first.Messages, "And now?")
	if err != nil {
		t.Fatalf("RunWithHistory() resume error = %v", err)
	}

	// The second request keeps exactly one result for the call: the
	// synthesized result is real history now, not repaired again.
	resume := client.requests[1]
	results := 0
	for _, message := range resume.Messages {
		if message.Role == model.RoleTool && message.ToolCallID == "call-1" {
			results++
			if message.Content != first.Messages[2].Content {
				t.Fatalf("resume result content = %q, want the first run's synthesized content", message.Content)
			}
		}
	}
	if results != 1 {
		t.Fatalf("results for call-1 in resumed request = %d, want 1", results)
	}
}

func TestRunWithHistoryRepairsOnlyUnansweredCalls(t *testing.T) {
	t.Parallel()

	history := []model.Message{
		{Role: model.RoleUser, Content: "Roll and report."},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "get_player_name", Args: json.RawMessage(`{}`)},
			{ID: "call-2", Name: "get_player_name", Args: json.RawMessage(`{}`)},
		}},
		{Role: model.RoleTool, ToolCallID: "call-1", ToolName: "get_player_name", Content: "Anne"},
	}
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	}}
	agent, err := golem.New[playerDeps, string](client, decoderOf(),
		golem.WithTools[playerDeps, string](toolForHistory(t)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.RunWithHistory(context.Background(), golem.RunContext[playerDeps]{}, history, "Continue")
	if err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}

	// Only call-2 is synthesized; call-1's real result is kept verbatim.
	request := client.requests[0]
	if len(request.Messages) != 5 {
		t.Fatalf("request messages = %#v", request.Messages)
	}
	results := request.Messages[2:4]
	if results[0].ToolCallID != "call-2" || !strings.Contains(results[0].Content, "interrupted before execution") {
		t.Fatalf("synthesized result = %#v", results[0])
	}
	if results[1].ToolCallID != "call-1" || results[1].Content != "Anne" {
		t.Fatalf("real result = %#v", results[1])
	}
}

func TestRunWithHistoryRepairsMidHistoryDanglingCallsInPlace(t *testing.T) {
	t.Parallel()

	history := []model.Message{
		{Role: model.RoleUser, Content: "Who is the player?"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "get_player_name", Args: json.RawMessage(`{}`)},
		}},
		{Role: model.RoleUser, Content: "Nevermind that."},
		{Role: model.RoleAssistant, Content: "Understood."},
	}
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	}}
	agent, err := golem.New[playerDeps, string](client, decoderOf(),
		golem.WithTools[playerDeps, string](toolForHistory(t)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.RunWithHistory(context.Background(), golem.RunContext[playerDeps]{}, history, "Continue")
	if err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}

	// The synthesized result follows the assistant message that requested
	// it, not the end of history: user, assistant-call, tool, user,
	// assistant, user prompt.
	request := client.requests[0]
	wantRoles := []model.Role{
		model.RoleUser, model.RoleAssistant, model.RoleTool,
		model.RoleUser, model.RoleAssistant, model.RoleUser,
	}
	if len(request.Messages) != len(wantRoles) {
		t.Fatalf("request messages = %#v", request.Messages)
	}
	for i, role := range wantRoles {
		if request.Messages[i].Role != role {
			t.Fatalf("request messages[%d].Role = %q, want %q", i, request.Messages[i].Role, role)
		}
	}
}

func TestRunWithHistoryDropsOrphanedToolResults(t *testing.T) {
	t.Parallel()

	history := []model.Message{
		{Role: model.RoleUser, Content: "Hello."},
		{Role: model.RoleTool, ToolCallID: "call-99", ToolName: "get_player_name", Content: "orphan"},
		{Role: model.RoleAssistant, Content: "Hi there."},
	}
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	}}
	agent, err := golem.New[playerDeps, string](client, decoderOf())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.RunWithHistory(context.Background(), golem.RunContext[playerDeps]{}, history, "Continue")
	if err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}

	// No provider accepts a result without its call, so it never reaches
	// the request.
	request := client.requests[0]
	wantRoles := []model.Role{model.RoleUser, model.RoleAssistant, model.RoleUser}
	if len(request.Messages) != len(wantRoles) {
		t.Fatalf("request messages = %#v", request.Messages)
	}
	for i, role := range wantRoles {
		if request.Messages[i].Role != role {
			t.Fatalf("request messages[%d].Role = %q, want %q", i, request.Messages[i].Role, role)
		}
	}
}
