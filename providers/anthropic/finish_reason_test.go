package anthropic_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestGenerateMapsStopReasons(t *testing.T) {
	t.Parallel()

	cases := []struct {
		wire string
		want model.FinishReason
	}{
		{"end_turn", model.FinishStop},
		{"stop_sequence", model.FinishStop},
		{"max_tokens", model.FinishLength},
		{"tool_use", model.FinishToolCall},
		{"refusal", model.FinishContentFilter},
		{"pause_turn", model.FinishOther},
		{"model_context_window_exceeded", model.FinishOther},
		{"", ""},
	}
	for _, tc := range cases {
		recorder := newRecordedServer(http.StatusOK, fmt.Sprintf(`{
			"content": [{"type": "text", "text": "x"}],
			"usage": {"input_tokens": 1, "output_tokens": 1},
			"stop_reason": %q
		}`, tc.wire))
		client := newClient(t, recorder.server.URL)

		response, err := client.Generate(context.Background(), model.Request{
			Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("Generate(%q) error = %v", tc.wire, err)
		}
		if response.FinishReason != tc.want {
			t.Errorf("stop_reason %q mapped to %q, want %q", tc.wire, response.FinishReason, tc.want)
		}
		recorder.server.Close()
	}
}

func TestGenerateStreamCarriesStopReason(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		"event: message_start\n",
		`data: {"type":"message_start","message":{"usage":{"input_tokens":25,"output_tokens":1}}}`+"\n\n",
		"event: content_block_start\n",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`+"\n\n",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`+"\n\n",
		`data: {"type":"content_block_stop","index":0}`+"\n\n",
		`data: {"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":15}}`+"\n\n",
		`data: {"type":"message_stop"}`+"\n\n",
	)
	client := newClient(t, server.server.URL)

	response, err := client.GenerateStream(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}, nil)
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}
	if response.FinishReason != model.FinishLength {
		t.Fatalf("FinishReason = %q, want length", response.FinishReason)
	}
}
