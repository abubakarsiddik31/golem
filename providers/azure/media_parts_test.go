package azure_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/azure"
)

func TestGenerateMapsDocumentAndAudioParts(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{"choices": [{"message": {"role": "assistant", "content": "ok"}}]}`)
	defer recorder.server.Close()
	client := newClient(t, recorder.server.URL)

	if _, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{{
		Role:    model.RoleUser,
		Content: "summarize",
		Parts: []model.Part{
			model.DocumentData("application/pdf", []byte{1, 2}),
			model.AudioData("audio/wav", []byte{3, 4}),
		},
	}}}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	var sent struct {
		Messages []struct {
			Content []struct {
				Type       string `json:"type"`
				InputAudio *struct {
					Format string `json:"format"`
				} `json:"input_audio"`
				File *struct {
					FileData string `json:"file_data"`
					Filename string `json:"filename"`
				} `json:"file"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(recorder.last(t).body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	content := sent.Messages[0].Content
	if len(content) != 3 {
		t.Fatalf("content array length = %d, want 3", len(content))
	}
	if content[1].Type != "file" || content[1].File == nil ||
		content[1].File.FileData != "AQI=" || content[1].File.Filename != "document.pdf" {
		t.Fatalf("document part = %#v, want base64 file_data and a pdf filename", content[1])
	}
	if content[2].Type != "input_audio" || content[2].InputAudio == nil ||
		content[2].InputAudio.Format != "wav" {
		t.Fatalf("wav part = %#v, want wav format", content[2])
	}
}

func TestGenerateRejectsUnsupportedMediaPartsWithoutSending(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{"choices": []}`)
	defer recorder.server.Close()
	client := newClient(t, recorder.server.URL)

	cases := []struct {
		name string
		part model.Part
		want string
	}{
		{"document url", model.DocumentURL("https://example.com/a.pdf"), "URL"},
		{"non-pdf document", model.DocumentData("text/markdown", []byte{1}), "application/pdf"},
		{"unsupported audio", model.AudioData("audio/ogg", []byte{1}), "audio/wav"},
		{"video", model.VideoData("video/mp4", []byte{1}), "gemini"},
	}
	for _, tc := range cases {
		_, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{{
			Role:    model.RoleUser,
			Content: "what is this",
			Parts:   []model.Part{tc.part},
		}}})
		var decodeErr *azure.DecodeError
		if !errors.As(err, &decodeErr) {
			t.Fatalf("%s: error = %v, want *azure.DecodeError", tc.name, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error = %v, want it to mention %q", tc.name, err, tc.want)
		}
	}
	if got := recorder.requestCount(); got != 0 {
		t.Fatalf("server received %d requests, want 0", got)
	}
}
