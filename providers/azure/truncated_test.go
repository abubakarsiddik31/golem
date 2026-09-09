package azure_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestTruncatedArgumentsBecomeSendable(t *testing.T) {
	recorder := newRecordedServer(200, `{"id":"r","object":"chat.completion","created":1,"model":"gpt-5","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
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
			ToolCalls []struct {
				Function struct {
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls,omitempty"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(recorder.last(t).body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	args := sent.Messages[0].ToolCalls[0].Function.Arguments
	if !strings.Contains(args, "truncated_args") {
		t.Fatalf("arguments = %s, want the truncated_args wrapper", args)
	}
}
