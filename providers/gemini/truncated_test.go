package gemini_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestTruncatedArgumentsBecomeSendable(t *testing.T) {
	recorder := newRecordedServer(200, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}],"usageMetadata":{"totalTokenCount":1}}`)
	client := newClient(t, recorder.server.URL)

	// The replay path: an assistant turn whose functionCall arguments
	// were truncated mid-stream.
	_, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "roll", Args: json.RawMessage(`{"sides":`)},
		}},
	}})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if body := recorder.last(t).body; !strings.Contains(body, "truncated_args") {
		t.Fatalf("wire = %s, want the truncated_args wrapper", body)
	}
}
