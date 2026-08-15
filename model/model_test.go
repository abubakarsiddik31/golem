package model_test

import (
	"encoding/json"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestMessageCarriesToolCallsAlongsideContent(t *testing.T) {
	t.Parallel()

	args := json.RawMessage(`{"guess":4}`)
	message := model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "roll_dice", Args: args},
		},
	}

	if len(message.ToolCalls) != 1 {
		t.Fatalf("ToolCalls length = %d, want 1", len(message.ToolCalls))
	}
	call := message.ToolCalls[0]
	if call.ID != "call-1" || call.Name != "roll_dice" {
		t.Fatalf("ToolCall = %#v", call)
	}
	if string(call.Args) != `{"guess":4}` {
		t.Fatalf("Args = %s, want raw JSON unchanged", call.Args)
	}
	var decoded map[string]any
	if err := json.Unmarshal(call.Args, &decoded); err != nil {
		t.Fatalf("Args is not valid JSON: %v", err)
	}
	if decoded["guess"] != float64(4) {
		t.Fatalf("decoded args = %#v", decoded)
	}
}

func TestToolResultMessageCorrelatesWithCall(t *testing.T) {
	t.Parallel()

	result := model.Message{
		Role:       model.RoleTool,
		ToolCallID: "call-1",
		ToolName:   "roll_dice",
		Content:    "4",
	}

	if result.Role != model.RoleTool || result.ToolCallID != "call-1" || result.ToolName != "roll_dice" {
		t.Fatalf("tool result message = %#v", result)
	}
}

func TestRequestAdvertisesToolSpecsWithRawSchema(t *testing.T) {
	t.Parallel()

	schema := json.RawMessage(`{"type":"object","properties":{"guess":{"type":"integer"}}}`)
	request := model.Request{
		ToolSpecs: []model.ToolSpec{
			{Name: "roll_dice", Description: "Roll a six-sided die.", Schema: schema},
		},
	}

	spec := request.ToolSpecs[0]
	if spec.Name != "roll_dice" || spec.Description != "Roll a six-sided die." {
		t.Fatalf("ToolSpec = %#v", spec)
	}
	if string(spec.Schema) != string(schema) {
		t.Fatalf("Schema = %s, want raw JSON unchanged", spec.Schema)
	}
}

func TestTextOnlyMessagesRemainValidWithoutToolFields(t *testing.T) {
	t.Parallel()

	messages := []model.Message{
		{Role: model.RoleSystem, Content: "Be concise."},
		{Role: model.RoleUser, Content: "Hello"},
		{Role: model.RoleAssistant, Content: "Hi."},
	}

	for _, message := range messages {
		if message.ToolCalls != nil || message.ToolCallID != "" || message.ToolName != "" {
			t.Fatalf("text-only message gained tool fields: %#v", message)
		}
	}
}
