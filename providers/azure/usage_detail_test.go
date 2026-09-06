package azure_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestGenerateReportsUsageDetail(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"choices": [{"message": {"role": "assistant", "content": "x"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 4,
			"prompt_tokens_details": {"cached_tokens": 8},
			"completion_tokens_details": {"reasoning_tokens": 3}}
	}`)
	defer recorder.server.Close()
	client := newClient(t, recorder.server.URL)

	response, err := client.Generate(context.Background(), userPrompt())
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
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"x\"}}]}\n\n",
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4,\"prompt_tokens_details\":{\"cached_tokens\":8},\"completion_tokens_details\":{\"reasoning_tokens\":3}}}\n\n",
		"data: [DONE]\n\n",
	)
	client := newClient(t, server.server.URL)

	response, err := client.GenerateStream(context.Background(), userPrompt(), nil)
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}
	want := model.Usage{InputTokens: 10, OutputTokens: 4, CacheReadTokens: 8, ReasoningTokens: 3}
	if response.Usage != want {
		t.Fatalf("usage = %#v, want %#v", response.Usage, want)
	}
}
