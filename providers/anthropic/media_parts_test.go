package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/anthropic"
)

func TestGenerateMapsDocumentPartsToDocumentBlocks(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{"content": [{"type": "text", "text": "ok"}]}`)
	defer recorder.server.Close()
	client := newClient(t, recorder.server.URL)

	if _, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{{
		Role:    model.RoleUser,
		Content: "summarize",
		Parts: []model.Part{
			model.DocumentData("application/pdf", []byte{1, 2}),
			model.DocumentURL("https://example.com/report.pdf"),
		},
	}}}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	var sent struct {
		Messages []struct {
			Content []struct {
				Type   string `json:"type"`
				Source *struct {
					Type      string `json:"type"`
					URL       string `json:"url,omitempty"`
					MediaType string `json:"media_type,omitempty"`
					Data      string `json:"data,omitempty"`
				} `json:"source"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	content := sent.Messages[0].Content
	if len(content) != 3 {
		t.Fatalf("content array length = %d, want 3", len(content))
	}
	if content[1].Type != "document" || content[1].Source == nil ||
		content[1].Source.Type != "base64" || content[1].Source.MediaType != "application/pdf" ||
		content[1].Source.Data != "AQI=" {
		t.Fatalf("inline document block = %#v", content[1])
	}
	if content[2].Type != "document" || content[2].Source == nil ||
		content[2].Source.Type != "url" || content[2].Source.URL != "https://example.com/report.pdf" {
		t.Fatalf("url document block = %#v", content[2])
	}
}

func TestGenerateRejectsUnsupportedMediaPartsWithoutSending(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{"content": []}`)
	defer recorder.server.Close()
	client := newClient(t, recorder.server.URL)

	cases := []struct {
		name string
		part model.Part
		want string
	}{
		{"non-pdf document", model.DocumentData("text/plain", []byte{1}), "application/pdf"},
		{"audio", model.AudioData("audio/wav", []byte{1}), "image and document"},
		{"video", model.VideoData("video/mp4", []byte{1}), "image and document"},
	}
	for _, tc := range cases {
		_, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{{
			Role:    model.RoleUser,
			Content: "what is this",
			Parts:   []model.Part{tc.part},
		}}})
		var decodeErr *anthropic.DecodeError
		if !errors.As(err, &decodeErr) {
			t.Fatalf("%s: error = %v, want *anthropic.DecodeError", tc.name, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error = %v, want it to mention %q", tc.name, err, tc.want)
		}
	}
	if got := recorder.requestCount(); got != 0 {
		t.Fatalf("server received %d requests, want 0", got)
	}
}
