package golem_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

func bigTool(t *testing.T, text string, maxOutput int) tool.Tool[struct{}] {
	t.Helper()
	return tool.MustNew(tool.Tool[struct{}]{
		Name:           "big",
		Description:    "big",
		Schema:         json.RawMessage(`{"type":"object"}`),
		MaxOutputBytes: maxOutput,
		Exec: func(context.Context, struct{}, json.RawMessage) (tool.Result, error) {
			return tool.Result{Text: text, Parts: []model.Part{model.ImageData("image/png", []byte("png"))}}, nil
		},
	})
}

func toolMessage(t *testing.T, requests []model.Request) model.Message {
	t.Helper()
	for _, request := range requests {
		for _, message := range request.Messages {
			if message.Role == model.RoleTool {
				return message
			}
		}
	}
	t.Fatal("no tool message found")
	return model.Message{}
}

func TestToolOutputLimitTruncatesAndKeepsParts(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("a", 500) + "END"
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "big", Args: json.RawMessage(`{}`)},
		}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](bigTool(t, text, 0)),
		golem.WithToolOutputLimit[struct{}, string](80),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	message := toolMessage(t, client.requests)
	if len(message.Content) > 80 {
		t.Fatalf("tool content is %d bytes, over the 80-byte cap: %q", len(message.Content), message.Content)
	}
	if !strings.Contains(message.Content, "tool output truncated; original size") {
		t.Fatalf("tool content lacks the truncation marker: %q", message.Content)
	}
	if !strings.HasPrefix(message.Content, "aaa") || !strings.HasSuffix(message.Content, "END") {
		t.Fatalf("tool content did not keep head and tail: %q", message.Content)
	}
	if len(message.Parts) != 1 {
		t.Fatalf("parts were dropped: %#v", message.Parts)
	}
	if got := lastToolMessage(t, result.Messages); got != message.Content {
		t.Fatalf("persisted tool content = %q, want the reduced form %q", got, message.Content)
	}
}

func TestToolOutputLimitPerToolOverride(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("b", 500)
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "big", Args: json.RawMessage(`{}`)},
		}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](bigTool(t, text, 40)),
		golem.WithToolOutputLimit[struct{}, string](200),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := toolMessage(t, client.requests).Content; len(got) > 40 {
		t.Fatalf("per-tool cap ignored: %d bytes %q", len(got), got)
	}
}

func TestToolOutputLimitDisabled(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("c", 500)
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "big", Args: json.RawMessage(`{}`)},
		}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](bigTool(t, text, 0)),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := toolMessage(t, client.requests).Content; got != text {
		t.Fatalf("disabled limit changed content: %d bytes", len(got))
	}
}

func TestToolOutputLimitValidatesAndPreservesUTF8(t *testing.T) {
	t.Parallel()

	if _, err := golem.New[struct{}, string](&fakeModel{}, decoderOf(),
		golem.WithToolOutputLimit[struct{}, string](-1)); err == nil {
		t.Fatal("negative agent limit accepted")
	}
	if _, err := tool.New(tool.Tool[struct{}]{Name: "x", Schema: json.RawMessage(`{}`),
		MaxOutputBytes: -1, Exec: func(context.Context, struct{}, json.RawMessage) (tool.Result, error) {
			return tool.Text(""), nil
		}}); err == nil {
		t.Fatal("negative per-tool limit accepted")
	}

	text := strings.Repeat("é", 100)
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "big", Args: json.RawMessage(`{}`)},
		}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](bigTool(t, text, 0)),
		golem.WithToolOutputLimit[struct{}, string](61),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	got := toolMessage(t, client.requests).Content
	if len(got) > 61 {
		t.Fatalf("UTF-8 limit exceeded: %d bytes", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncation split a rune: %q", got)
	}
}

func lastToolMessage(t *testing.T, messages []model.Message) string {
	t.Helper()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == model.RoleTool {
			return messages[i].Content
		}
	}
	t.Fatal("no tool message")
	return ""
}
