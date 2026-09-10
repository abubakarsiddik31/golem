package bedrock_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestTruncatedArgumentsBecomeSendable(t *testing.T) {
	recorder := newRecordedServer(200, `{"output":{"message":{"role":"assistant","content":[{"text":"ok"}]}},"stopReason":"end_turn","usage":{}}`, nil)
	client := newClient(t, recorder.server.URL)

	_, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "roll", Args: json.RawMessage(`{"sides":`)},
		}},
	}})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	var sent struct {
		Messages []struct {
			Content []struct {
				ToolUse *struct {
					Input json.RawMessage `json:"input"`
				} `json:"toolUse,omitempty"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(recorder.last(t).body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	var input string
	for _, turn := range sent.Messages {
		for _, block := range turn.Content {
			if block.ToolUse != nil {
				input = string(block.ToolUse.Input)
			}
		}
	}
	if !strings.Contains(input, "truncated_args") {
		t.Fatalf("toolUse input = %s, want the truncated_args wrapper", input)
	}
}
