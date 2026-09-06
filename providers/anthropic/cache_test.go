package anthropic_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/anthropic"
)

func TestAutomaticCachingRidesEveryRequest(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		ttl      time.Duration
		wantType string
		wantTTL  string
	}{
		{"default five-minute entry", 0, "ephemeral", ""},
		{"one-hour entry", anthropic.CacheOneHour, "ephemeral", "1h"},
	}
	for _, tc := range cases {
		recorder := newRecordedServer(http.StatusOK, `{
			"content": [{"type": "text", "text": "x"}],
			"usage": {"input_tokens": 1, "output_tokens": 1},
			"stop_reason": "end_turn"
		}`)
		client, err := anthropic.New(anthropic.Config{
			APIKey: "test-key", BaseURL: recorder.server.URL, Model: "claude-sonnet-4-5",
			CacheControl: &anthropic.CacheControl{TTL: tc.ttl},
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
			CacheControl *map[string]any `json:"cache_control"`
		}
		if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
			t.Fatalf("%s: request body is not valid JSON: %v", tc.name, err)
		}
		if sent.CacheControl == nil {
			t.Fatalf("%s: cache_control missing from the request", tc.name)
		}
		ttl, _ := (*sent.CacheControl)["ttl"].(string)
		if (*sent.CacheControl)["type"] != tc.wantType || ttl != tc.wantTTL {
			t.Fatalf("%s: cache_control = %v, want type %q ttl %q", tc.name, *sent.CacheControl, tc.wantType, tc.wantTTL)
		}
		recorder.server.Close()
	}
}

func TestCachingOffByDefault(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"content": [{"type": "text", "text": "x"}],
		"usage": {"input_tokens": 1, "output_tokens": 1},
		"stop_reason": "end_turn"
	}`)
	defer recorder.server.Close()
	client := newClient(t, recorder.server.URL)

	if _, err := client.Generate(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if strings.Contains(recorder.lastBody(t), "cache_control") {
		t.Fatalf("cache_control on the wire without configuration: %s", recorder.lastBody(t))
	}
}

func TestNewRejectsUnsupportedCacheTTL(t *testing.T) {
	t.Parallel()

	_, err := anthropic.New(anthropic.Config{
		APIKey: "key", Model: "claude-sonnet-4-5",
		CacheControl: &anthropic.CacheControl{TTL: 30 * time.Minute},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported cache TTL") {
		t.Fatalf("New() error = %v, want an unsupported-TTL rejection", err)
	}
}
