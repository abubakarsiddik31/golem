package bedrock_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestGenerateReportsUsageDetail(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"output": {"message": {"role": "assistant", "content": [{"text": "x"}]}},
		"stopReason": "end_turn",
		"usage": {"inputTokens": 10, "outputTokens": 4,
			"cacheReadInputTokens": 8, "cacheWriteInputTokens": 2}
	}`, nil)
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

	server := streamServer(t, http.StatusOK,
		eventFrame("messageStart", `{"role":"assistant"}`),
		eventFrame("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"x"}}`),
		eventFrame("contentBlockStop", `{"contentBlockIndex":0}`),
		eventFrame("messageStop", `{"stopReason":"end_turn"}`),
		eventFrame("metadata", `{"usage":{"inputTokens":10,"outputTokens":4,"cacheReadInputTokens":8,"cacheWriteInputTokens":2}}`),
	)
	defer server.server.Close()
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
