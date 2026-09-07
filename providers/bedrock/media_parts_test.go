package bedrock_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/bedrock"
)

func TestGenerateMapsDocumentPartsToDocumentBlocks(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"output": {"message": {"role": "assistant", "content": [{"text": "ok"}]}}
	}`, nil)
	defer recorder.server.Close()
	client := newClient(t, recorder.server.URL)

	if _, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{{
		Role:    model.RoleUser,
		Content: "summarize",
		Parts: []model.Part{
			model.DocumentData("application/pdf", []byte{1, 2}),
			model.DocumentData("text/plain", []byte{3, 4}),
		},
	}}}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	var sent struct {
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(recorder.last(t).body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	wantBlocks := []map[string]any{
		{"text": "summarize"},
		{"document": map[string]any{
			"name":   "document-1",
			"format": "pdf",
			"source": map[string]any{"bytes": "AQI="},
		}},
		{"document": map[string]any{
			"name":   "document-2",
			"format": "txt",
			"source": map[string]any{"bytes": "AwQ="},
		}},
	}
	got := sent.Messages[0].Content
	if len(got) != len(wantBlocks) {
		t.Fatalf("user turn blocks = %#v, want %d blocks", got, len(wantBlocks))
	}
	for i, want := range wantBlocks {
		if !reflect.DeepEqual(got[i], want) {
			t.Fatalf("block[%d] = %#v, want %#v", i, got[i], want)
		}
	}
}

func TestGenerateRejectsUnsupportedMediaPartsWithoutSending(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{"output": {}}`, nil)
	defer recorder.server.Close()
	client := newClient(t, recorder.server.URL)

	cases := []struct {
		name string
		part model.Part
		want string
	}{
		{"document url", model.DocumentURL("https://example.com/a.pdf"), "URL"},
		{"unknown document media type", model.DocumentData("application/zip", []byte{1}), "application/pdf"},
		{"audio", model.AudioData("audio/wav", []byte{1}), "image and document"},
		{"video", model.VideoData("video/mp4", []byte{1}), "image and document"},
	}
	for _, tc := range cases {
		_, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{{
			Role:    model.RoleUser,
			Content: "what is this",
			Parts:   []model.Part{tc.part},
		}}})
		if !errors.Is(err, bedrock.ErrUnsupportedContent) {
			t.Fatalf("%s: error = %v, want ErrUnsupportedContent", tc.name, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error = %v, want it to mention %q", tc.name, err, tc.want)
		}
	}
	if request := recorder.lastOptional(); request != "" {
		t.Fatalf("adapter sent a request despite rejecting the content: %s", request)
	}
}
