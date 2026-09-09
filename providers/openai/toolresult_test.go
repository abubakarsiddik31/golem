package openai_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestToolResultFramesPartsOntoTheUserChannel(t *testing.T) {
	recorder := newRecordedServer(200, `{"id":"r","object":"chat.completion","created":1,"model":"gpt-5","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	client := newClient(t, recorder.server.URL)

	_, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "look"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call-1", Name: "camera"}, {ID: "call-2", Name: "scanner"}}},
		{Role: model.RoleTool, ToolCallID: "call-1", ToolName: "camera", Content: "scanned",
			Parts: []model.Part{model.ImageData("image/png", []byte{1})}},
		{Role: model.RoleTool, ToolCallID: "call-2", ToolName: "scanner", Content: "nothing here"},
	}})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	var sent struct {
		Messages []struct {
			Role       string          `json:"role"`
			Content    json.RawMessage `json:"content,omitempty"`
			ToolCallID string          `json:"tool_call_id,omitempty"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}

	// Order: user, assistant, tool result for call-1, tool result for
	// call-2, then ONE synthetic user message carrying the framed media.
	roles := make([]string, 0, len(sent.Messages))
	for _, m := range sent.Messages {
		roles = append(roles, m.Role)
	}
	want := "user,assistant,tool,tool,user"
	if got := strings.Join(roles, ","); got != want {
		t.Fatalf("wire roles = %s, want %s (%s)", got, want, recorder.lastBody(t))
	}
	framed := sent.Messages[len(sent.Messages)-1]
	var contentParts []struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		ImageURL *struct {
			URL string `json:"url"`
		} `json:"image_url,omitempty"`
	}
	if err := json.Unmarshal(framed.Content, &contentParts); err != nil {
		t.Fatalf("framed content is not a content array: %v (%s)", err, framed.Content)
	}
	if len(contentParts) != 3 || contentParts[0].Type != "text" ||
		!strings.Contains(contentParts[0].Text, `"camera"`) ||
		!strings.Contains(contentParts[0].Text, "call-1") ||
		contentParts[1].ImageURL == nil ||
		contentParts[2].Text != "\n</tool_result>" {
		t.Fatalf("framed content = %#v, want the attribution frame, the image, and the closing tag", contentParts)
	}
}

func TestToolFailureFramesAnErrorObject(t *testing.T) {
	recorder := newRecordedServer(200, `{"id":"r","object":"chat.completion","created":1,"model":"gpt-5","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	client := newClient(t, recorder.server.URL)

	_, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call-1", Name: "archive"}}},
		{Role: model.RoleTool, ToolCallID: "call-1", ToolName: "archive", Content: "mailbox is read-only", Failed: true},
	}})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if !strings.Contains(recorder.lastBody(t), `{"error":"mailbox is read-only"}`) {
		t.Fatalf("tool message = %s, want the JSON-framed failure", recorder.lastBody(t))
	}
}
