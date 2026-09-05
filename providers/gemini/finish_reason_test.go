package gemini_test

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
		{"STOP", model.FinishStop},
		{"MAX_TOKENS", model.FinishLength},
		{"SAFETY", model.FinishContentFilter},
		{"RECITATION", model.FinishContentFilter},
		{"BLOCKLIST", model.FinishContentFilter},
		{"PROHIBITED_CONTENT", model.FinishContentFilter},
		{"SPII", model.FinishContentFilter},
		{"MALFORMED_FUNCTION_CALL", model.FinishOther},
		{"", ""},
	}
	for _, tc := range cases {
		recorder := newRecordedServer(http.StatusOK, fmt.Sprintf(`{
			"candidates": [{"content": {"role": "model", "parts": [{"text": "x"}]}, "finishReason": %q}],
			"usageMetadata": {"promptTokenCount": 1, "candidatesTokenCount": 1}
		}`, tc.wire))
		client := newClient(t, recorder.server.URL)

		response, err := client.Generate(context.Background(), model.Request{
			Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("Generate(%q) error = %v", tc.wire, err)
		}
		if response.FinishReason != tc.want {
			t.Errorf("finishReason %q mapped to %q, want %q", tc.wire, response.FinishReason, tc.want)
		}
		recorder.server.Close()
	}
}

func TestGenerateStreamCarriesFinishReason(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		"data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"lo\"}]}}]}\n\n",
		"data: {\"candidates\":[{\"finishReason\":\"SAFETY\"}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":2}}\n\n",
	)
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
