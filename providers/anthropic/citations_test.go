package anthropic_test

import (
	"context"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/anthropic"
)

const citedResponse = `{
  "content": [
    {"type":"text","text":"Returns are accepted within 30 days.","citations":[
      {"type":"char_location","cited_text":"within 30 days","document_index":0,
       "document_title":"Return policy","start_char_index":12,"end_char_index":26}
    ]},
    {"type":"text","text":"See the web page.","citations":[
      {"type":"web_search_result_location","cited_text":"policy page","url":"https://example.com/returns",
       "title":"Returns","start_char_index":0,"end_char_index":11}
    ]}
  ],
  "usage": {"input_tokens": 10, "output_tokens": 8},
  "stop_reason": "end_turn"
}`

func TestGenerateParsesCitationsAndRequestsThem(t *testing.T) {
	t.Parallel()

	server := newRecordedServer(200, citedResponse)
	defer server.server.Close()
	client, err := anthropic.New(anthropic.Config{
		APIKey:    "key",
		BaseURL:   server.server.URL,
		Model:     "claude-sonnet-4-5",
		Citations: true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := model.Request{Messages: []model.Message{{
		Role:    model.RoleUser,
		Content: "How long do I have to return an item?",
		Parts:   []model.Part{model.DocumentData("application/pdf", []byte("policy"))},
	}}}
	response, err := client.Generate(context.Background(), request)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(response.Message.Citations) != 2 {
		t.Fatalf("citations = %#v", response.Message.Citations)
	}
	first := response.Message.Citations[0]
	if first.Type != "char_location" || first.Text != "within 30 days" || first.Title != "Return policy" ||
		first.StartCharIndex != 12 || first.EndCharIndex != 26 {
		t.Fatalf("first citation = %#v", first)
	}
	second := response.Message.Citations[1]
	if second.URL != "https://example.com/returns" || second.Title != "Returns" {
		t.Fatalf("second citation = %#v", second)
	}
	body := server.lastBody(t)
	if !strings.Contains(body, `"citations":{"enabled":true}`) {
		t.Fatalf("document block did not request citations: %s", body)
	}
}

func TestGenerateLeavesCitationsOffByDefault(t *testing.T) {
	t.Parallel()

	server := newRecordedServer(200, citedResponse)
	defer server.server.Close()
	client, err := anthropic.New(anthropic.Config{APIKey: "key", BaseURL: server.server.URL, Model: "claude-sonnet-4-5"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := model.Request{Messages: []model.Message{{
		Role:  model.RoleUser,
		Parts: []model.Part{model.DocumentData("application/pdf", []byte("policy"))},
	}}}
	if _, err := client.Generate(context.Background(), request); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if body := server.lastBody(t); strings.Contains(body, `"citations":{"enabled":true}`) {
		t.Fatalf("citations enabled without opt-in: %s", body)
	}
}

func TestGenerateRejectsMalformedCitations(t *testing.T) {
	t.Parallel()

	server := newRecordedServer(200, `{"content":[{"type":"text","text":"x","citations":{}}],"stop_reason":"end_turn"}`)
	defer server.server.Close()
	client, err := anthropic.New(anthropic.Config{APIKey: "key", BaseURL: server.server.URL, Model: "claude-sonnet-4-5"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = client.Generate(context.Background(), model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}})
	if err == nil || !strings.Contains(err.Error(), "citations") {
		t.Fatalf("Generate() error = %v, want a citation decode error", err)
	}
}

func TestGenerateStreamCollectsCitations(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`+"\n\n",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`+"\n\n",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Returns are accepted."}}`+"\n\n",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"citations_delta","citation":{"type":"char_location","cited_text":"within 30 days","document_title":"Return policy","start_char_index":12,"end_char_index":26}}}`+"\n\n",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`+"\n\n",
		`data: {"type":"message_stop"}`+"\n\n",
	)
	defer server.server.Close()
	client, err := anthropic.New(anthropic.Config{APIKey: "key", BaseURL: server.server.URL, Model: "claude-sonnet-4-5"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	response, err := client.GenerateStream(context.Background(), userPrompt(), nil)
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}
	if len(response.Message.Citations) != 1 {
		t.Fatalf("citations = %#v", response.Message.Citations)
	}
	if got := response.Message.Citations[0]; got.Text != "within 30 days" || got.Title != "Return policy" {
		t.Fatalf("citation = %#v", got)
	}
}
