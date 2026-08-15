package golem_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

func toolForHistory(t *testing.T) tool.Tool[playerDeps] {
	t.Helper()
	return tool.MustNew(tool.Tool[playerDeps]{
		Name:        "get_player_name",
		Description: "Get the player's name.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps playerDeps, args json.RawMessage) (string, error) {
			return deps.Name, nil
		},
	})
}

func TestRunWithHistoryContinuesTheConversation(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "first answer"}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "second answer"}},
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithInstructions[struct{}, string]("current instructions"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	first, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "first question")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	second, err := agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{},
		first.Messages, "second question")
	if err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}

	// The continued request is one conversation: instructions, the prior
	// exchange, and the new prompt.
	request := client.requests[1]
	wantRoles := []model.Role{
		model.RoleSystem, model.RoleUser, model.RoleAssistant, model.RoleUser,
	}
	if len(request.Messages) != len(wantRoles) {
		t.Fatalf("continued request messages = %#v", request.Messages)
	}
	for i, role := range wantRoles {
		if request.Messages[i].Role != role {
			t.Fatalf("continued request messages[%d].Role = %q, want %q", i, request.Messages[i].Role, role)
		}
	}
	if request.Messages[0].Content != "current instructions" {
		t.Fatalf("instructions = %q", request.Messages[0].Content)
	}
	if request.Messages[1].Content != "first question" || request.Messages[2].Content != "first answer" {
		t.Fatalf("prior exchange = %#v", request.Messages[1:3])
	}
	if request.Messages[3].Content != "second question" {
		t.Fatalf("new prompt = %q", request.Messages[3].Content)
	}

	// The second result carries the full chained conversation.
	if second.Output != "second answer" {
		t.Fatalf("Output = %q", second.Output)
	}
	if len(second.Messages) != 5 || second.Messages[4].Content != "second answer" {
		t.Fatalf("chained evidence = %#v", second.Messages)
	}
}

func TestRunWithHistoryReplacesStaleInstructions(t *testing.T) {
	t.Parallel()

	history := []model.Message{
		{Role: model.RoleSystem, Content: "stale instructions"},
		{Role: model.RoleUser, Content: "earlier question"},
		{Role: model.RoleAssistant, Content: "earlier answer"},
	}
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "fresh answer"}},
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithInstructions[struct{}, string]("fresh instructions"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{}, history, "new question")
	if err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}

	messages := client.requests[0].Messages
	if len(messages) != 4 {
		t.Fatalf("request messages = %#v", messages)
	}
	system := 0
	for _, message := range messages {
		if message.Role == model.RoleSystem {
			system++
			if message.Content != "fresh instructions" {
				t.Fatalf("system message = %q, want current instructions", message.Content)
			}
		}
	}
	if system != 1 {
		t.Fatalf("system messages = %d, want exactly the current instructions", system)
	}
}

func TestRunWithHistoryWithoutInstructionsKeepsHistoryVerbatim(t *testing.T) {
	t.Parallel()

	history := []model.Message{
		{Role: model.RoleUser, Content: "earlier question"},
		{Role: model.RoleAssistant, Content: "earlier answer"},
	}
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "answer"}},
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{}, history, "new question")
	if err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}

	messages := client.requests[0].Messages
	wantRoles := []model.Role{model.RoleUser, model.RoleAssistant, model.RoleUser}
	if len(messages) != len(wantRoles) {
		t.Fatalf("request messages = %#v", messages)
	}
	for i, role := range wantRoles {
		if messages[i].Role != role {
			t.Fatalf("messages[%d].Role = %q, want %q", i, messages[i].Role, role)
		}
	}
}

func TestRunWithHistoryExecutesToolsMidConversation(t *testing.T) {
	t.Parallel()

	history := []model.Message{
		{Role: model.RoleUser, Content: "I need help with my account."},
		{Role: model.RoleAssistant, Content: "Ask away."},
	}
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "get_player_name", Args: json.RawMessage(`{}`)},
		}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "Anne is the player"}},
	}}
	getName := toolForHistory(t)
	agent, err := golem.New[playerDeps, string](client, decoderOf(),
		golem.WithTools[playerDeps, string](getName))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.RunWithHistory(context.Background(),
		golem.RunContext[playerDeps]{Deps: playerDeps{Name: "Anne"}}, history, "Who is the player?")
	if err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}

	// Prior turns, new prompt, the tool exchange, and the final answer, in
	// order: user, assistant, user, assistant-call, tool, assistant-final.
	wantRoles := []model.Role{
		model.RoleUser, model.RoleAssistant, model.RoleUser,
		model.RoleAssistant, model.RoleTool, model.RoleAssistant,
	}
	if len(result.Messages) != len(wantRoles) {
		t.Fatalf("evidence = %#v", result.Messages)
	}
	for i, role := range wantRoles {
		if result.Messages[i].Role != role {
			t.Fatalf("evidence[%d].Role = %q, want %q", i, result.Messages[i].Role, role)
		}
	}
	if result.Messages[4].Content != "Anne" || result.Messages[4].ToolName != "get_player_name" {
		t.Fatalf("tool result = %#v", result.Messages[4])
	}
	if result.Output != "Anne is the player" {
		t.Fatalf("Output = %q", result.Output)
	}
}
