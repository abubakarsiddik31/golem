package gemini_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestGenerateMapsDocumentAudioVideoParts(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{"candidates": [{"content": {"parts": [{"text": "ok"}], "role": "model"}}]}`)
	defer recorder.server.Close()
	client := newClient(t, recorder.server.URL)

	if _, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{{
		Role:    model.RoleUser,
		Content: "what is in these",
		Parts: []model.Part{
			model.DocumentData("application/pdf", []byte{1, 2}),
			model.AudioData("audio/wav", []byte{3, 4}),
			model.VideoData("video/mp4", []byte{5, 6}),
			model.DocumentURL("https://example.com/report.pdf"),
		},
	}}}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	var sent struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text       string `json:"text,omitempty"`
				InlineData *struct {
					MimeType string `json:"mimeType"`
					Data     string `json:"data"`
				} `json:"inlineData"`
				FileData *struct {
					FileURI  string `json:"fileUri"`
					MimeType string `json:"mimeType"`
				} `json:"fileData"`
			} `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal([]byte(recorder.last(t).body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	var user *struct {
		Role  string `json:"role"`
		Parts []struct {
			Text       string `json:"text,omitempty"`
			InlineData *struct {
				MimeType string `json:"mimeType"`
				Data     string `json:"data"`
			} `json:"inlineData"`
			FileData *struct {
				FileURI  string `json:"fileUri"`
				MimeType string `json:"mimeType"`
			} `json:"fileData"`
		} `json:"parts"`
	}
	for i := range sent.Contents {
		if sent.Contents[i].Role == "user" {
			user = &sent.Contents[i]
		}
	}
	if user == nil {
		t.Fatalf("no user turn in request: %s", recorder.last(t).body)
	}
	if len(user.Parts) != 5 {
		t.Fatalf("user parts length = %d, want 5: %s", len(user.Parts), recorder.last(t).body)
	}
	if user.Parts[1].InlineData == nil || user.Parts[1].InlineData.MimeType != "application/pdf" || user.Parts[1].InlineData.Data != "AQI=" {
		t.Fatalf("document part = %#v", user.Parts[1])
	}
	if user.Parts[2].InlineData == nil || user.Parts[2].InlineData.MimeType != "audio/wav" || user.Parts[2].InlineData.Data != "AwQ=" {
		t.Fatalf("audio part = %#v", user.Parts[2])
	}
	if user.Parts[3].InlineData == nil || user.Parts[3].InlineData.MimeType != "video/mp4" || user.Parts[3].InlineData.Data != "BQY=" {
		t.Fatalf("video part = %#v", user.Parts[3])
	}
	if user.Parts[4].FileData == nil || user.Parts[4].FileData.FileURI != "https://example.com/report.pdf" || user.Parts[4].FileData.MimeType != "" {
		t.Fatalf("url part = %#v, want fileUri without a media type", user.Parts[4])
	}
}
