package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/openai"
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
			model.AudioData("audio/mpeg", []byte{5, 6}),
		},
	}}}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	var sent struct {
		Messages []struct {
			Content []struct {
				Type       string `json:"type"`
				InputAudio *struct {
					Data   string `json:"data"`
					Format string `json:"format"`
				} `json:"input_audio"`
				File *struct {
					FileData string `json:"file_data"`
					Filename string `json:"filename"`
				} `json:"file"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	content := sent.Messages[0].Content
	if len(content) != 4 {
		t.Fatalf("content array length = %d, want 4", len(content))
	}
	if content[0].Type != "text" {
		t.Fatalf("first part = %s, want text", content[0].Type)
	}
	if content[1].Type != "file" || content[1].File == nil ||
		content[1].File.FileData != "AQI=" || content[1].File.Filename != "document.pdf" {
		t.Fatalf("document part = %#v, want base64 file_data and a pdf filename", content[1])
	}
	if content[2].Type != "input_audio" || content[2].InputAudio == nil ||
		content[2].InputAudio.Data != "AwQ=" || content[2].InputAudio.Format != "wav" {
		t.Fatalf("wav part = %#v, want base64 data with wav format", content[2])
	}
	if content[3].Type != "input_audio" || content[3].InputAudio == nil ||
		content[3].InputAudio.Format != "mp3" {
		t.Fatalf("mp3 part = %#v, want mp3 format", content[3])
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
		{"audio url", model.Part{Kind: model.PartAudio, URL: "https://example.com/a.wav"}, "URL"},
		{"unsupported audio", model.AudioData("audio/ogg", []byte{1}), "audio/wav"},
		{"video", model.VideoData("video/mp4", []byte{1}), "gemini"},
	}
	for _, tc := range cases {
		_, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{{
			Role:    model.RoleUser,
			Content: "what is this",
			Parts:   []model.Part{tc.part},
		}}})
		var decodeErr *openai.DecodeError
		if !errors.As(err, &decodeErr) {
			t.Fatalf("%s: error = %v, want *openai.DecodeError", tc.name, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error = %v, want it to mention %q", tc.name, err, tc.want)
		}
	}
	if got := recorder.requestCount(); got != 0 {
		t.Fatalf("server received %d requests, want 0", got)
	}
}
