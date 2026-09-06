package bedrock_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/bedrock"
)

func TestCachePointRidesTheConversationFrontier(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		ttl     time.Duration
		wantTTL string
	}{
		{"default five-minute entry", 0, ""},
		{"one-hour entry", bedrock.CacheOneHour, "1h"},
	}
	for _, tc := range cases {
		recorder := newRecordedServer(http.StatusOK, `{
			"output": {"message": {"role": "assistant", "content": [{"text": "x"}]}},
			"stopReason": "end_turn",
			"usage": {"inputTokens": 1, "outputTokens": 1}
		}`, nil)
		client, err := bedrock.New(bedrock.Config{
			Credentials: bedrock.Credentials{AccessKeyID: "id", SecretAccessKey: "secret"},
			Region:      "us-east-1", Model: "m", BaseURL: recorder.server.URL,
			CacheControl: &bedrock.CacheControl{TTL: tc.ttl},
		})
		if err != nil {
			t.Fatalf("%s: New() error = %v", tc.name, err)
		}

		_, err = client.Generate(context.Background(), model.Request{
			Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("%s: Generate() error = %v", tc.name, err)
		}

		var sent struct {
			Messages []struct {
				Role    string `json:"role"`
				Content []struct {
					CachePoint *struct {
						Type string `json:"type"`
						TTL  string `json:"ttl"`
					} `json:"cachePoint"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal([]byte(recorder.last(t).body), &sent); err != nil {
			t.Fatalf("%s: request body is not valid JSON: %v", tc.name, err)
		}
		if len(sent.Messages) == 0 {
			t.Fatalf("%s: no messages on the wire", tc.name)
		}
		last := sent.Messages[len(sent.Messages)-1]
		if len(last.Content) == 0 || last.Content[len(last.Content)-1].CachePoint == nil {
			t.Fatalf("%s: no cachePoint on the final block: %v", tc.name, last)
		}
		point := last.Content[len(last.Content)-1].CachePoint
		if point.Type != "default" || point.TTL != tc.wantTTL {
			t.Fatalf("%s: cachePoint = %+v, want type default ttl %q", tc.name, point, tc.wantTTL)
		}
		recorder.server.Close()
	}
}

func TestCachingOffByDefault(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"output": {"message": {"role": "assistant", "content": [{"text": "x"}]}},
		"stopReason": "end_turn",
		"usage": {"inputTokens": 1, "outputTokens": 1}
	}`, nil)
	defer recorder.server.Close()
	client := newClient(t, recorder.server.URL)

	if _, err := client.Generate(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if strings.Contains(recorder.last(t).body, "cachePoint") {
		t.Fatalf("cachePoint on the wire without configuration: %s", recorder.last(t).body)
	}
}

func TestNewRejectsUnsupportedCacheTTL(t *testing.T) {
	t.Parallel()

	_, err := bedrock.New(bedrock.Config{
		Credentials: bedrock.Credentials{AccessKeyID: "id", SecretAccessKey: "secret"},
		Region:      "us-east-1", Model: "m",
		CacheControl: &bedrock.CacheControl{TTL: 30 * time.Minute},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported cache TTL") {
		t.Fatalf("New() error = %v, want an unsupported-TTL rejection", err)
	}
}
