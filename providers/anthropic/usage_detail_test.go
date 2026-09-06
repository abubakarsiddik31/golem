package anthropic_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestGenerateReportsUsageDetail(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"content": [{"type": "text", "text": "x"}],
		"usage": {"input_tokens": 10, "output_tokens": 4,
			"cache_read_input_tokens": 8, "cache_creation_input_tokens": 2},
		"stop_reason": "end_turn"
	}`)
	defer recorder.server.Close()
	client := newClient(t, recorder.server.URL)

	response, err := client.Generate(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	want := model.Usage{InputTokens: 10, OutputTokens: 4, CacheReadTokens: 8, CacheWriteTokens: 2}
	if response.Usage != want {
		t.Fatalf("usage = %#v, want %#v", response.Usage, want)
	}
}

func TestGenerateStreamReportsUsageDetail(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		"event: message_start\n",
		`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1,"cache_read_input_tokens":8,"cache_creation_input_tokens":2}}}`+"\n\n",
		"event: content_block_start\n",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`+"\n\n",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`+"\n\n",
		`data: {"type":"content_block_stop","index":0}`+"\n\n",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`+"\n\n",
		`data: {"type":"message_stop"}`+"\n\n",
	)
	client := newClient(t, server.server.URL)

	response, err := client.GenerateStream(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}, nil)
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}
	want := model.Usage{InputTokens: 10, OutputTokens: 4, CacheReadTokens: 8, CacheWriteTokens: 2}
	if response.Usage != want {
		t.Fatalf("usage = %#v, want %#v", response.Usage, want)
	}
}
