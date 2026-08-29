package model_test

import (
	"bytes"
	"encoding/json"
	"strings"
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

	// The exact wire shape of the first three messages; field names are a
	// durable, additive-only contract.
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

func TestImagePartsValidateAtTheBoundary(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		part model.Part
		want string
	}{
		{"url and data", model.Part{Kind: model.PartImage, URL: "https://example.com/y.png", Data: []byte{1}}, "both"},
		{"neither url nor data", model.Part{Kind: model.PartImage}, "neither"},
		{"data without media type", model.Part{Kind: model.PartImage, Data: []byte{1}}, "media type"},
		{"unknown kind", model.Part{Kind: "video", URL: "https://example.com/y.mp4"}, "unsupported part kind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.part.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want error mentioning %q", err, tc.want)
			}
		})
	}

	if err := (model.ImageURL("https://example.com/a.png")).Validate(); err != nil {
		t.Fatalf("ImageURL part invalid: %v", err)
	}
	if err := (model.ImageData("image/png", []byte{1, 2, 3})).Validate(); err != nil {
		t.Fatalf("ImageData part invalid: %v", err)
	}
}

func TestMessageJSONStaysAdditiveWithParts(t *testing.T) {
	t.Parallel()

	// Existing text-only encoding must stay byte-identical: the JSON
	// shape is a durable, additive-only contract.
	textOnly := model.Message{Role: model.RoleUser, Content: "hi"}
	encoded, err := json.Marshal(textOnly)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if want := `{"role":"user","content":"hi"}`; string(encoded) != want {
		t.Fatalf("text-only encoding changed:\n got %s\nwant %s", encoded, want)
	}

	withURL := model.Message{Role: model.RoleUser, Content: "look",
		Parts: []model.Part{model.ImageURL("https://example.com/cat.png")}}
	encoded, err = json.Marshal(withURL)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	want := `{"role":"user","content":"look","parts":[{"kind":"image","url":"https://example.com/cat.png"}]}`
	if string(encoded) != want {
		t.Fatalf("url part encoding:\n got %s\nwant %s", encoded, want)
	}

	// Inline data encodes base64 and round-trips.
	withData := model.Message{Role: model.RoleUser,
		Parts: []model.Part{model.ImageData("image/png", []byte{1, 2, 3})}}
	encoded, err = json.Marshal(withData)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded model.Message
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.Parts[0].Kind != model.PartImage || decoded.Parts[0].MediaType != "image/png" ||
		!bytes.Equal(decoded.Parts[0].Data, []byte{1, 2, 3}) {
		t.Fatalf("inline data did not round-trip: %#v", decoded.Parts[0])
	}

	// History persisted before parts exists decodes unchanged.
	var legacy model.Message
	if err := json.Unmarshal([]byte(`{"role":"user","content":"hi"}`), &legacy); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if legacy.Parts != nil {
		t.Fatalf("legacy message gained parts: %#v", legacy.Parts)
	}
}

func TestThinkingBlocksValidateAtTheBoundary(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		block model.ThinkingBlock
		want  string
	}{
		{"neither text nor redacted", model.ThinkingBlock{}, "neither"},
		{"text and redacted", model.ThinkingBlock{Text: "why", Redacted: "enc"}, "exactly one"},
		{"signature on redacted", model.ThinkingBlock{Redacted: "enc", Signature: "sig"}, "signature"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.block.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want error mentioning %q", err, tc.want)
			}
		})
	}

	for name, block := range map[string]model.ThinkingBlock{
		"text":     model.ThinkingText("reasoning"),
		"signed":   model.ThinkingSigned("reasoning", "sig"),
		"redacted": model.ThinkingRedacted("enc"),
	} {
		if err := block.Validate(); err != nil {
			t.Fatalf("%s constructor produced invalid block: %v", name, err)
		}
	}
}

func TestMessageJSONStaysAdditiveWithThinking(t *testing.T) {
	t.Parallel()

	// Existing text-only encoding must stay byte-identical: the JSON
	// shape is a durable, additive-only contract.
	textOnly := model.Message{Role: model.RoleAssistant, Content: "hi"}
	encoded, err := json.Marshal(textOnly)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if want := `{"role":"assistant","content":"hi"}`; string(encoded) != want {
		t.Fatalf("text-only encoding changed:\n got %s\nwant %s", encoded, want)
	}

	// A thinking turn carries blocks in order and a signed tool call.
	withThinking := model.Message{
		Role:    model.RoleAssistant,
		Content: "4",
		Thinking: []model.ThinkingBlock{
			model.ThinkingSigned("2+2", "sig-1"),
			model.ThinkingRedacted("enc"),
		},
		ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "roll_dice", Args: json.RawMessage(`{}`), Signature: "callsig"},
		},
	}
	encoded, err = json.Marshal(withThinking)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	want := `{"role":"assistant","content":"4","toolCalls":[{"id":"call-1","name":"roll_dice","args":{},"signature":"callsig"}],` +
		`"thinking":[{"text":"2+2","signature":"sig-1"},{"redacted":"enc"}]}`
	if string(encoded) != want {
		t.Fatalf("thinking encoding:\n got %s\nwant %s", encoded, want)
	}

	// History persisted before thinking exists decodes unchanged.
	var legacy model.Message
	if err := json.Unmarshal([]byte(`{"role":"assistant","content":"hi"}`), &legacy); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if legacy.Thinking != nil {
		t.Fatalf("legacy message gained thinking: %#v", legacy.Thinking)
	}
	var decoded model.Message
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(decoded.Thinking) != 2 || decoded.Thinking[0].Text != "2+2" ||
		decoded.Thinking[0].Signature != "sig-1" || decoded.Thinking[1].Redacted != "enc" {
		t.Fatalf("thinking did not round-trip: %#v", decoded.Thinking)
	}
	if len(decoded.ToolCalls) != 1 || decoded.ToolCalls[0].Signature != "callsig" {
		t.Fatalf("tool call signature did not round-trip: %#v", decoded.ToolCalls)
	}
}
