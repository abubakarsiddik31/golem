package gemini_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestGenerateReportsUsageDetail(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"candidates": [{"content": {"role": "model", "parts": [{"text": "x"}]}, "finishReason": "STOP"}],
		"usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 4,
			"cachedContentTokenCount": 8, "thoughtsTokenCount": 3}
	}`)
	defer recorder.server.Close()
	client := newClient(t, recorder.server.URL)

	response, err := client.Generate(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	want := model.Usage{InputTokens: 10, OutputTokens: 4, CacheReadTokens: 8, ReasoningTokens: 3}
	if response.Usage != want {
		t.Fatalf("usage = %#v, want %#v", response.Usage, want)
	}
}

func TestGenerateStreamReportsUsageDetail(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		"data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"x\"}]}}]}\n\n",
		"data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":4,\"cachedContentTokenCount\":8,\"thoughtsTokenCount\":3}}\n\n",
	)
	client := newClient(t, server.server.URL)

	response, err := client.GenerateStream(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}, nil)
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}
	want := model.Usage{InputTokens: 10, OutputTokens: 4, CacheReadTokens: 8, ReasoningTokens: 3}
	if response.Usage != want {
		t.Fatalf("usage = %#v, want %#v", response.Usage, want)
	}
}
