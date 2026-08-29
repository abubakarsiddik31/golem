package golem_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

// trimHistory builds a history whose shape exercises TrimHistory's
// boundary rules: a user turn, an assistant tool-call turn answered by a
// result, then later plain turns.
func trimHistoryFixture() []model.Message {
	return []model.Message{
		{Role: model.RoleUser, Content: "oldest"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call-1", Name: "get_player_name"}}},
		{Role: model.RoleTool, ToolCallID: "call-1", ToolName: "get_player_name", Content: "Ada"},
		{Role: model.RoleUser, Content: "middle"},
		{Role: model.RoleAssistant, Content: "middle answer"},
		{Role: model.RoleUser, Content: "newest"},
	}
}

func TestTrimHistoryKeepsNewestMessagesAtTurnBoundaries(t *testing.T) {
	t.Parallel()

	history := trimHistoryFixture()

	// A budget of 4 keeps [tool, user, assistant, user]; the tool result
	// whose call was trimmed cannot open the request, so the boundary
	// rule advances to the user turn that can.
	trimmed, err := golem.TrimHistory(4)(context.Background(), history)
	if err != nil {
		t.Fatalf("TrimHistory(4) error = %v", err)
	}
	if len(trimmed) != 3 {
		t.Fatalf("trimmed length = %d, want 3 (user, assistant, user)", len(trimmed))
	}
	if trimmed[0].Content != "middle" || trimmed[1].Content != "middle answer" || trimmed[2].Content != "newest" {
		t.Fatalf("trimmed = %#v, want the newest three-message turn span", trimmed)
	}

	// A budget at or above the history length changes nothing.
	same, err := golem.TrimHistory(len(history))(context.Background(), history)
	if err != nil {
		t.Fatalf("TrimHistory(len) error = %v", err)
	}
	if len(same) != len(history) {
		t.Fatalf("oversized budget trimmed %d to %d messages", len(history), len(same))
	}

	// The boundary rule also skips a leading assistant tool-call turn,
	// whose results were trimmed: repair would otherwise synthesize them.
	callFirst := []model.Message{
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call-9", Name: "get_player_name"}}},
		{Role: model.RoleUser, Content: "keep me"},
	}
	trimmed, err = golem.TrimHistory(1)(context.Background(), callFirst)
	if err != nil {
		t.Fatalf("TrimHistory(1) error = %v", err)
	}
	if len(trimmed) != 1 || trimmed[0].Content != "keep me" {
		t.Fatalf("trimmed = %#v, want only the user message", trimmed)
	}
}

func TestTrimHistoryRejectsDegenerateInput(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	if _, err := golem.TrimHistory(0)(ctx, trimHistoryFixture()); err == nil {
		t.Fatal("zero budget accepted")
	}
	toolOnly := []model.Message{
		{Role: model.RoleTool, ToolCallID: "call-1", Content: "x"},
		{Role: model.RoleTool, ToolCallID: "call-2", Content: "y"},
		{Role: model.RoleTool, ToolCallID: "call-3", Content: "z"},
	}
	if _, err := golem.TrimHistory(2)(ctx, toolOnly); err == nil {
		t.Fatal("all-tool-result cut accepted; nothing can open a conversation")
	}
}

func TestRunWithHistoryProcessorSendsProcessedHistory(t *testing.T) {
	t.Parallel()

	client := &fakeModel{response: model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "done"},
	}}
	agent, err := golem.New[struct{}, string](
		client,
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithHistoryProcessor[struct{}, string](golem.TrimHistory(4)),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{},
		trimHistoryFixture(), "next"); err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}

	sent := client.request.Messages
	// no instructions configured: trimmed history(3) + fresh prompt(1)
	if len(sent) != 4 {
		t.Fatalf("request carries %d messages, want 4: %#v", len(sent), sent)
	}
	if sent[0].Content != "middle" || sent[1].Content != "middle answer" || sent[2].Content != "newest" {
		t.Fatalf("history not trimmed to the newest turn span: %#v", sent[:3])
	}
}

func TestRunWithHistoryProcessorFailsBeforeModelCall(t *testing.T) {
	t.Parallel()

	client := &fakeModel{}
	agent, err := golem.New[struct{}, string](
		client,
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithHistoryProcessor[struct{}, string](func(ctx context.Context, history []model.Message) ([]model.Message, error) {
			return nil, errors.New("cannot compress")
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hi")
	if err == nil || !strings.Contains(err.Error(), "history processor") {
		t.Fatalf("Run() error = %v, want the processor failure", err)
	}
	if client.request.Messages != nil {
		t.Fatal("model was called despite the processor failing")
	}
}

func TestHistoryProcessorRunsBeforePartValidation(t *testing.T) {
	t.Parallel()

	client := &fakeModel{response: model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "ok"},
	}}
	agent, err := golem.New[struct{}, string](
		client,
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithHistoryProcessor[struct{}, string](golem.TrimHistory(1)),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// The assistant parts would fail validation, but the processor trims
	// them away first: only the newest user message survives.
	history := []model.Message{
		{Role: model.RoleAssistant, Content: "old", Parts: []model.Part{model.ImageURL("https://example.com/a.png")}},
		{Role: model.RoleUser, Content: "keep"},
	}
	if _, err := agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{}, history, "next"); err != nil {
		t.Fatalf("RunWithHistory() error = %v; trimmed history should not be part-validated", err)
	}
}

func TestRunWithHistoryRoundTripsThinking(t *testing.T) {
	t.Parallel()

	client := &fakeModel{response: model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "42"},
	}}
	agent, err := golem.New[struct{}, string](
		client,
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// A resumed conversation carries the previous turn's reasoning; the
	// adapter replays it verbatim so the provider verifies the thinking.
	history := []model.Message{
		{Role: model.RoleUser, Content: "what is 2+2?"},
		{Role: model.RoleAssistant, Content: "2+2 is 4",
			Thinking:  []model.ThinkingBlock{model.ThinkingSigned("adding", "sig-1"), model.ThinkingRedacted("enc")},
			ToolCalls: []model.ToolCall{{ID: "call-1", Name: "calc", Args: json.RawMessage(`{}`), Signature: "callsig"}},
		},
		{Role: model.RoleTool, ToolCallID: "call-1", ToolName: "calc", Content: "4"},
	}
	if _, err := agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{}, history, "and 3 more?"); err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}

	sent := client.request.Messages
	if len(sent) != 4 {
		t.Fatalf("sent %d messages, want 4 (history + prompt)", len(sent))
	}
	assistant := sent[1]
	if len(assistant.Thinking) != 2 {
		t.Fatalf("assistant thinking blocks = %d, want 2", len(assistant.Thinking))
	}
	if assistant.Thinking[0].Text != "adding" || assistant.Thinking[0].Signature != "sig-1" ||
		assistant.Thinking[1].Redacted != "enc" {
		t.Fatalf("thinking blocks did not round-trip: %#v", assistant.Thinking)
	}
	if assistant.ToolCalls[0].Signature != "callsig" {
		t.Fatalf("tool call signature did not round-trip: %#v", assistant.ToolCalls[0])
	}
}

func TestRunWithHistoryRejectsMisplacedThinking(t *testing.T) {
	t.Parallel()

	client := &fakeModel{response: model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "ok"},
	}}
	agent, err := golem.New[struct{}, string](
		client,
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Thinking belongs to assistant messages; anywhere else fails the run
	// before any model call.
	history := []model.Message{
		{Role: model.RoleUser, Content: "hi", Thinking: []model.ThinkingBlock{model.ThinkingText("why")}},
	}
	_, err = agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{}, history, "next")
	if err == nil || !strings.Contains(err.Error(), "thinking is only supported on assistant messages") {
		t.Fatalf("RunWithHistory() error = %v, want misplaced-thinking validation error", err)
	}

	// A malformed block inside an assistant turn also fails validation,
	// and neither failure reaches the model.
	history = []model.Message{
		{Role: model.RoleAssistant, Content: "hi", Thinking: []model.ThinkingBlock{{}}},
	}
	_, err = agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{}, history, "next")
	if err == nil || !strings.Contains(err.Error(), "thinking block 0") {
		t.Fatalf("RunWithHistory() error = %v, want malformed-block validation error", err)
	}

	if len(client.request.Messages) != 0 {
		t.Fatalf("model received %d requests; validation must fail before any model call", len(client.request.Messages))
	}
}
