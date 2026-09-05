package openai_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestGenerateMapsFinishReasons(t *testing.T) {
	t.Parallel()

	cases := []struct {
		wire string
		want model.FinishReason
	}{
		{"stop", model.FinishStop},
		{"length", model.FinishLength},
		{"tool_calls", model.FinishToolCall},
		{"function_call", model.FinishToolCall},
		{"content_filter", model.FinishContentFilter},
		{"some_new_cause", model.FinishOther},
		{"", ""},
	}
	for _, tc := range cases {
		recorder := newRecordedServer(http.StatusOK, fmt.Sprintf(`{
			"choices": [{"message": {"role": "assistant", "content": "x"}, "finish_reason": %q}],
			"usage": {"prompt_tokens": 1, "completion_tokens": 1}
		}`, tc.wire))
		client := newClient(t, recorder.server.URL)

		response, err := client.Generate(context.Background(), userPrompt())
		if err != nil {
			t.Fatalf("Generate(%q) error = %v", tc.wire, err)
		}
		if response.FinishReason != tc.want {
			t.Errorf("finish_reason %q mapped to %q, want %q", tc.wire, response.FinishReason, tc.want)
		}
		recorder.server.Close()
	}
}

func TestGenerateStreamCarriesFinishReason(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Hel\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"length\"}]}\n\n",
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\n",
		"data: [DONE]\n\n",
	)
	client := newClient(t, server.server.URL)

	response, err := client.GenerateStream(context.Background(), userPrompt(), nil)
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}
	if response.FinishReason != model.FinishLength {
		t.Fatalf("FinishReason = %q, want length", response.FinishReason)
	}
}
