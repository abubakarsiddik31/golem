package bedrock_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestToolResultCarriesPartsAndFailure(t *testing.T) {
	recorder := newRecordedServer(200, `{"output":{"message":{"role":"assistant","content":[{"text":"ok"}]}},"stopReason":"end_turn","usage":{}}`, nil)
	client := newClient(t, recorder.server.URL)

	_, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "look"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call-1", Name: "camera"}}},
		{Role: model.RoleTool, ToolCallID: "call-1", ToolName: "camera", Content: "scanned", Failed: true,
			Parts: []model.Part{
				model.ImageData("image/png", []byte{1}),
				model.DocumentData("application/pdf", []byte{2}),
			}},
	}})
	if err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	var sent struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				ToolResult *struct {
					ToolUseID string `json:"toolUseId"`
					Status    string `json:"status,omitempty"`
					Content   []struct {
						Text     string           `json:"text,omitempty"`
						Image    *json.RawMessage `json:"image,omitempty"`
						Document *json.RawMessage `json:"document,omitempty"`
					} `json:"content"`
				} `json:"toolResult,omitempty"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(recorder.last(t).body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}

	var result *struct {
		ToolUseID string
		Status    string
		Blocks    int
	}
	for _, turn := range sent.Messages {
		for _, block := range turn.Content {
			if block.ToolResult == nil {
				continue
			}
			if result != nil {
				t.Fatal("multiple toolResult blocks")
			}
			result = &struct {
				ToolUseID string
				Status    string
				Blocks    int
			}{block.ToolResult.ToolUseID, block.ToolResult.Status, len(block.ToolResult.Content)}
		}
	}
	if result == nil {
		t.Fatalf("no toolResult block in %s", recorder.last(t).body)
	}
	if result.ToolUseID != "call-1" || result.Status != "error" {
		t.Fatalf("toolResult = %#v, want call-1 with status error", result)
	}
	if result.Blocks != 3 {
		t.Fatalf("toolResult content blocks = %d, want text plus image plus document", result.Blocks)
	}
	body := recorder.last(t).body
	if !strings.Contains(body, `"image"`) || !strings.Contains(body, `"document"`) {
		t.Fatalf("toolResult content missing image and document blocks: %s", body)
	}
}

func TestToolResultRejectsUnsendableParts(t *testing.T) {
	client := newClient(t, "http://127.0.0.1:1")
	_, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call-1", Name: "mic"}}},
		{Role: model.RoleTool, ToolCallID: "call-1", ToolName: "mic",
			Parts: []model.Part{model.AudioData("audio/wav", []byte{1})}},
	}})
	if err == nil || !strings.Contains(err.Error(), "tool result") {
		t.Fatalf("Converse() error = %v, want rejection of audio inside a tool result", err)
	}
}
