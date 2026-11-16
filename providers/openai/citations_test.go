package openai_test

import (
	"context"
	"testing"
)

func TestGenerateParsesURLCitationAnnotations(t *testing.T) {
	t.Parallel()

	server := newRecordedServer(200, `{
	  "choices": [{"message": {"role": "assistant", "content": "Answer", "annotations": [
	    {"type": "url_citation", "url_citation": {"url": "https://example.com/doc", "title": "Doc", "start_index": 0, "end_index": 6}}
	  ]}, "finish_reason": "stop"}],
	  "usage": {"prompt_tokens": 3, "completion_tokens": 2}
	}`)
	defer server.server.Close()
	client := newClient(t, server.server.URL)
	response, err := client.Generate(context.Background(), userPrompt())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(response.Message.Citations) != 1 {
		t.Fatalf("citations = %#v", response.Message.Citations)
	}
	citation := response.Message.Citations[0]
	if citation.Type != "url_citation" || citation.URL != "https://example.com/doc" ||
		citation.Title != "Doc" || citation.StartCharIndex != 0 || citation.EndCharIndex != 6 {
		t.Fatalf("citation = %#v", citation)
	}
}

func TestGenerateStreamCollectsAnnotationCitations(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		`data: {"choices":[{"delta":{"role":"assistant","content":"Answer","annotations":[{"type":"url_citation","url_citation":{"url":"https://example.com/doc","title":"Doc","start_index":0,"end_index":6}}]}}]}`+"\n\n",
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n",
		`data: {"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2}}`+"\n\n",
		`data: [DONE]`+"\n\n",
	)
	client := newClient(t, server.server.URL)
	response, err := client.GenerateStream(context.Background(), userPrompt(), nil)
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}
	if len(response.Message.Citations) != 1 || response.Message.Citations[0].URL != "https://example.com/doc" {
		t.Fatalf("citations = %#v", response.Message.Citations)
	}
}
