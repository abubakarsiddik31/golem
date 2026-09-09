package anthropic_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/anthropic"
)

func TestToolResultCarriesPartsAndFailure(t *testing.T) {
	recorder := newRecordedServer(200, `{"content":[{"type":"text","text":"ok"}],"usage":{},"stop_reason":"end_turn"}`)
	client := newClient(t, recorder.server.URL)

	_, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "look"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call-1", Name: "camera"}}},
		{Role: model.RoleTool, ToolCallID: "call-1", ToolName: "camera", Content: "no such file", Failed: true,
			Parts: []model.Part{
				model.ImageData("image/png", []byte{1}),
				model.DocumentData("application/pdf", []byte{2}),
			}},
	}})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	var sent struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type      string `json:"type"`
				Text      string `json:"text,omitempty"`
				ToolUseID string `json:"tool_use_id,omitempty"`
				IsError   bool   `json:"is_error,omitempty"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}

	// The tool result rides a user turn as a tool_result block whose
	// content array carries the text, the image, and the document, with
	// is_error set for the definitive failure.
	var result *struct {
		Type      string `json:"type"`
		Text      string `json:"text,omitempty"`
		ToolUseID string `json:"tool_use_id,omitempty"`
		IsError   bool   `json:"is_error,omitempty"`
	}
	for i, turn := range sent.Messages {
		for j, block := range turn.Content {
			if block.Type != "tool_result" {
				continue
			}
			if result != nil {
				t.Fatalf("multiple tool_result blocks: turn %d block %d", i, j)
			}
			block := block
			result = &block
		}
	}
	if result == nil {
		t.Fatalf("no tool_result block in %#v", sent.Messages)
	}
	if result.ToolUseID != "call-1" || !result.IsError {
		t.Fatalf("tool_result = %#v, want call-1 flagged as an error", result)
	}
	if !strings.Contains(recorder.lastBody(t), `"image"`) || !strings.Contains(recorder.lastBody(t), `"document"`) {
		t.Fatalf("tool_result content missing the image and document blocks: %s", recorder.lastBody(t))
	}
}

func TestToolResultRejectsUnsendableParts(t *testing.T) {
	client, err := anthropic.New(anthropic.Config{APIKey: "k", Model: "claude-sonnet-4-5"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = client.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call-1", Name: "mic"}}},
		{Role: model.RoleTool, ToolCallID: "call-1", ToolName: "mic",
			Parts: []model.Part{model.AudioData("audio/wav", []byte{1})}},
	}})
	if err == nil || !strings.Contains(err.Error(), "tool result") {
		t.Fatalf("Generate() error = %v, want rejection of audio inside a tool result", err)
	}
}
