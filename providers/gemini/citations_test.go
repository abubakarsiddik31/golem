package gemini_test

import (
	"context"
	"testing"

	"github.com/abubakarsiddik31/golem/providers/gemini"
)

const groundingResponse = `{
  "candidates": [{
    "content": {"parts": [{"text": "Answer"}]},
    "finishReason": "STOP",
    "groundingMetadata": {
      "groundingChunks": [
        {"web": {"uri": "https://a.example", "title": "A"}},
        {"web": {"uri": "https://b.example", "title": "B"}}
      ],
      "groundingSupports": [{
        "segment": {"startIndex": 0, "endIndex": 6, "text": "Answer"},
        "groundingChunkIndices": [0, 1]
      }]
    }
  }],
  "usageMetadata": {"promptTokenCount": 5, "candidatesTokenCount": 2}
}`

func TestGenerateParsesGroundingCitations(t *testing.T) {
	t.Parallel()

	server := newRecordedServer(200, groundingResponse)
	defer server.server.Close()
	client, err := gemini.New(gemini.Config{APIKey: "k", BaseURL: server.server.URL, Model: "gemini-2.5-flash"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	response, err := client.Generate(context.Background(), userPrompt())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(response.Message.Citations) != 2 {
		t.Fatalf("citations = %#v", response.Message.Citations)
	}
	first := response.Message.Citations[0]
	if first.Type != "grounding" || first.URL != "https://a.example" || first.Title != "A" ||
		first.Text != "Answer" || first.StartCharIndex != 0 || first.EndCharIndex != 6 {
		t.Fatalf("first citation = %#v", first)
	}
	if second := response.Message.Citations[1]; second.URL != "https://b.example" {
		t.Fatalf("second citation = %#v", second)
	}
}

func TestGenerateStreamCollectsGroundingCitations(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		`data: {"candidates":[{"content":{"parts":[{"text":"Answer"}]},"groundingMetadata":{"groundingChunks":[{"web":{"uri":"https://a.example","title":"A"}}],"groundingSupports":[{"segment":{"startIndex":0,"endIndex":6,"text":"Answer"},"groundingChunkIndices":[0]}]}}]}`+"\n\n",
		`data: {"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2}}`+"\n\n",
	)
	client := newClient(t, server.server.URL)
	response, err := client.GenerateStream(context.Background(), userPrompt(), nil)
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}
	if len(response.Message.Citations) != 1 || response.Message.Citations[0].URL != "https://a.example" {
		t.Fatalf("citations = %#v", response.Message.Citations)
	}
}
