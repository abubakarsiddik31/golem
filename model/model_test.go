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

// conversation is a full exchange exercising every message shape.
func conversation() []model.Message {
	return []model.Message{
		{Role: model.RoleSystem, Content: "Be concise."},
		{Role: model.RoleUser, Content: "Roll the dice."},
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call-1", Name: "roll_dice", Args: json.RawMessage(`{"guess":4}`)},
			},
		},
		{Role: model.RoleTool, ToolCallID: "call-1", ToolName: "roll_dice", Content: "4"},
		{Role: model.RoleAssistant, Content: "You rolled a 4."},
	}
}

func TestMessagesRoundTripThroughJSON(t *testing.T) {
	t.Parallel()

	original := conversation()

	payload, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var restored []model.Message
	if err := json.Unmarshal(payload, &restored); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if len(restored) != len(original) {
		t.Fatalf("round trip length = %d, want %d", len(restored), len(original))
	}
	for i, want := range original {
		got := restored[i]
		if got.Role != want.Role || got.Content != want.Content ||
			got.ToolCallID != want.ToolCallID || got.ToolName != want.ToolName {
			t.Fatalf("round trip message[%d] = %#v, want %#v", i, got, want)
		}
		if len(got.ToolCalls) != len(want.ToolCalls) {
			t.Fatalf("round trip message[%d] tool calls = %#v, want %#v", i, got.ToolCalls, want.ToolCalls)
		}
		for j, wantCall := range want.ToolCalls {
			gotCall := got.ToolCalls[j]
			if gotCall.ID != wantCall.ID || gotCall.Name != wantCall.Name {
				t.Fatalf("round trip call[%d][%d] = %#v, want %#v", i, j, gotCall, wantCall)
			}
			if string(gotCall.Args) != string(wantCall.Args) {
				t.Fatalf("round trip args = %s, want byte-exact %s", gotCall.Args, wantCall.Args)
			}
		}
	}
}

func TestMessageJSONShapeIsPinned(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(conversation())
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	// The exact wire shape of the first three messages; ADR 0005 pins it as
	// additive-only, so field names here are contract.
	wantPrefix := `[{"role":"system","content":"Be concise."},` +
		`{"role":"user","content":"Roll the dice."},` +
		`{"role":"assistant","toolCalls":[{"id":"call-1","name":"roll_dice","args":{"guess":4}}]},` +
		`{"role":"tool","content":"4","toolCallId":"call-1","toolName":"roll_dice"},` +
		`{"role":"assistant","content":"You rolled a 4."}`
	if string(payload) != wantPrefix+"]" {
		t.Fatalf("marshaled shape = %s, want %s", payload, wantPrefix+"]")
	}
}

func TestUnmarshalToleratesUnknownAndMissingFields(t *testing.T) {
	t.Parallel()

	// A payload written by a future version (extra field) and an older one
	// (no optional fields) must both decode: the shape is additive-only.
	payload := `[{"role":"user","content":"hi","futureField":true},{"role":"assistant"}]`
	var messages []model.Message
	if err := json.Unmarshal([]byte(payload), &messages); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if messages[0].Role != model.RoleUser || messages[0].Content != "hi" {
		t.Fatalf("message[0] = %#v", messages[0])
	}
	if messages[1].Role != model.RoleAssistant || messages[1].Content != "" || messages[1].ToolCalls != nil {
		t.Fatalf("message[1] = %#v, want zero optional fields", messages[1])
	}
}
