package anthropic_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestTruncatedArgumentsBecomeSendable(t *testing.T) {
	recorder := newRecordedServer(200, `{"content":[{"type":"text","text":"ok"}],"usage":{},"stop_reason":"end_turn"}`)
	client := newClient(t, recorder.server.URL)

	_, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "roll", Args: json.RawMessage(`{"sides":`)},
		}},
	}})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	body := recorder.lastBody(t)
	if !strings.Contains(body, `"truncated_args"`) || !strings.Contains(body, "sides") {
		t.Fatalf("wire = %s, want the verbatim bytes wrapped as truncated_args", body)
	}
	// The wrapper is an object, so the request encodes cleanly — the
	// recorded body parsed as JSON above proves it.
}
