package bedrock_test

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
		{"guardrail_intervened", model.FinishContentFilter},
		{"content_filtered", model.FinishContentFilter},
		{"some_new_cause", model.FinishOther},
		{"", ""},
	}
	for _, tc := range cases {
		recorder := newRecordedServer(http.StatusOK, fmt.Sprintf(`{
			"output": {"message": {"role": "assistant", "content": [{"text": "x"}]}},
			"stopReason": %q,
			"usage": {"inputTokens": 1, "outputTokens": 1}
		}`, tc.wire), nil)
		client := newClient(t, recorder.server.URL)

		response, err := client.Generate(context.Background(), model.Request{
			Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("Generate(%q) error = %v", tc.wire, err)
		}
		if response.FinishReason != tc.want {
			t.Errorf("stopReason %q mapped to %q, want %q", tc.wire, response.FinishReason, tc.want)
		}
		recorder.server.Close()
	}
}

func TestGenerateStreamCarriesStopReason(t *testing.T) {
	t.Parallel()

	server := streamServer(t, http.StatusOK,
		eventFrame("messageStart", `{"role":"assistant"}`),
		eventFrame("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"lo"}}`),
		eventFrame("contentBlockStop", `{"contentBlockIndex":0}`),
		eventFrame("messageStop", `{"stopReason":"guardrail_intervened"}`),
		eventFrame("metadata", `{"usage":{"inputTokens":4,"outputTokens":6}}`),
	)
	defer server.server.Close()
	client := newClient(t, server.server.URL)

	response, err := client.GenerateStream(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}, nil)
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}
	if response.FinishReason != model.FinishContentFilter {
		t.Fatalf("FinishReason = %q, want content_filter", response.FinishReason)
	}
}
