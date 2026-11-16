package bedrock_test

import (
	"context"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/bedrock"
)

const citedResponse = `{
  "output": {"message": {"role": "assistant", "content": [
    {"text": "Returns are accepted within 30 days."},
    {"citationsContent": {
      "content": [{"text": "within 30 days"}],
      "citations": [{
        "location": {"documentChar": {"documentIndex": 0, "start": 12, "end": 26}, "type": "document_char"},
        "sourceContent": [{"text": "within 30 days"}]
      }]
    }}
  ]}},
  "stopReason": "end_turn",
  "usage": {"inputTokens": 10, "outputTokens": 8}
}`

func TestGenerateParsesDocumentCitationsAndRequestsThem(t *testing.T) {
	t.Parallel()

	server := newRecordedServer(200, citedResponse, nil)
	defer server.server.Close()
	client, err := bedrock.New(bedrock.Config{
		Credentials: bedrock.Credentials{AccessKeyID: "id", SecretAccessKey: "secret"},
		Region:      "us-east-1",
		Model:       "anthropic.claude-sonnet-4-5",
		BaseURL:     server.server.URL,
		Citations:   true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := model.Request{Messages: []model.Message{{
		Role:  model.RoleUser,
		Parts: []model.Part{model.DocumentData("application/pdf", []byte("policy"))},
	}}}
	response, err := client.Generate(context.Background(), request)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if response.Message.Content != "Returns are accepted within 30 days.\nwithin 30 days" {
		t.Fatalf("content = %q", response.Message.Content)
	}
	if len(response.Message.Citations) != 1 {
		t.Fatalf("citations = %#v", response.Message.Citations)
	}
	citation := response.Message.Citations[0]
	if citation.Type != "document_char" || citation.Text != "within 30 days" ||
		citation.StartCharIndex != 12 || citation.EndCharIndex != 26 {
		t.Fatalf("citation = %#v", citation)
	}
	if body := server.last(t).body; !strings.Contains(body, `"citations":{"enabled":true}`) {
		t.Fatalf("document block did not request citations: %s", body)
	}
}

func TestGenerateLeavesBedrockCitationsOffByDefault(t *testing.T) {
	t.Parallel()

	server := newRecordedServer(200, `{"output":{"message":{"role":"assistant","content":[{"text":"ok"}]}},"stopReason":"end_turn"}`, nil)
	defer server.server.Close()
	client, err := bedrock.New(bedrock.Config{
		Credentials: bedrock.Credentials{AccessKeyID: "id", SecretAccessKey: "secret"},
		Region:      "us-east-1",
		Model:       "anthropic.claude-sonnet-4-5",
		BaseURL:     server.server.URL,
	})
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
	if body := server.last(t).body; strings.Contains(body, `"citations":{"enabled":true}`) {
		t.Fatalf("citations enabled without opt-in: %s", body)
	}
}
