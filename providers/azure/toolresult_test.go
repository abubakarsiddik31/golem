package azure_test

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
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call-1", Name: "camera"}}},
		{Role: model.RoleTool, ToolCallID: "call-1", ToolName: "camera", Content: "scanned",
			Parts: []model.Part{model.ImageData("image/png", []byte{1})}},
	}})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	var sent struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content,omitempty"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(recorder.last(t).body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}

	if len(sent.Messages) != 3 || sent.Messages[2].Role != "user" {
		t.Fatalf("wire roles = %#v, want the framed user message last", sent.Messages)
	}
	body := string(sent.Messages[2].Content)
	var contentParts []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	}
	if err := json.Unmarshal(sent.Messages[2].Content, &contentParts); err != nil {
		t.Fatalf("framed content is not a content array: %v (%s)", err, body)
	}
	if len(contentParts) != 3 || !strings.Contains(contentParts[0].Text, "camera") ||
		!strings.Contains(body, "image_url") {
		t.Fatalf("framed content = %s, want the frame, the image, and the closing tag", body)
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

	if !strings.Contains(recorder.last(t).body, `{"error":"mailbox is read-only"}`) {
		t.Fatalf("tool message = %s, want the JSON-framed failure", recorder.last(t).body)
	}
}
