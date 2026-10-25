package openai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/openai"
)

// thinkStreamServer streams content fragments, one chunk per fragment.
func thinkStreamServer(t *testing.T, fragments ...string) *openai.Client {
	t.Helper()
	chunks := make([]string, 0, len(fragments)+2)
	for _, fragment := range fragments {
		encoded, _ := json.Marshal(fragment)
		chunks = append(chunks,
			"data: {\"choices\":[{\"delta\":{\"content\":"+string(encoded)+"}}]}\n\n")
	}
	chunks = append(chunks, "data: [DONE]\n\n")
	server := newSSEServer(t, chunks...)
	return newClient(t, server.server.URL)
}

// TestGenerateStreamExtractsLeadingThinkBlock feeds the tag split
// across fragment boundaries, the way a real stream arrives.
func TestGenerateStreamExtractsLeadingThinkBlock(t *testing.T) {
	t.Parallel()

	client := thinkStreamServer(t,
		"<thi", "nk>I should ", "check the units.</th", "ink>\n\n", "The length is 4 meters.",
	)

	var contentFragments, thinkFragments []string
	response, err := client.GenerateStream(context.Background(), userPrompt(), func(d model.Delta) error {
		if d.Content != "" {
			contentFragments = append(contentFragments, d.Content)
		}
		for _, think := range d.Thinking {
			thinkFragments = append(thinkFragments, think.Text)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}
	if joined := strings.Join(contentFragments, ""); joined != "The length is 4 meters." {
		t.Errorf("content fragments = %#v, joined %q", contentFragments, joined)
	}
	if joined := strings.Join(thinkFragments, ""); joined != "I should check the units." {
		t.Errorf("thinking fragments = %#v, joined %q", thinkFragments, joined)
	}
	if len(response.Message.Thinking) != 1 || response.Message.Thinking[0].Text != "I should check the units." {
		t.Errorf("assembled thinking = %#v", response.Message.Thinking)
	}
	if response.Message.Content != "The length is 4 meters." {
		t.Errorf("assembled content = %q", response.Message.Content)
	}
}

// TestGenerateStreamKeepsNonThinkContent verifies the conservative
// path: content that does not open with the tag passes through whole,
// including a literal tag later in the message.
func TestGenerateStreamKeepsNonThinkContent(t *testing.T) {
	t.Parallel()

	client := thinkStreamServer(t,
		"XML tags like ", "<think> are just markup in this answer.",
	)

	var thinkSeen bool
	response, err := client.GenerateStream(context.Background(), userPrompt(), func(d model.Delta) error {
		if len(d.Thinking) > 0 {
			thinkSeen = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}
	if thinkSeen {
		t.Error("a non-think message produced thinking deltas")
	}
	if want := "XML tags like <think> are just markup in this answer."; response.Message.Content != want {
		t.Errorf("content = %q, want %q", response.Message.Content, want)
	}
	if len(response.Message.Thinking) != 0 {
		t.Errorf("thinking = %#v, want none", response.Message.Thinking)
	}
}

// TestGenerateStreamUnclosedThinkBlockIsReasoning verifies the
// truncated-phase rule.
func TestGenerateStreamUnclosedThinkBlockIsReasoning(t *testing.T) {
	t.Parallel()

	client := thinkStreamServer(t, "<think>", "reasoning that never closes")

	response, err := client.GenerateStream(context.Background(), userPrompt(), nil)
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}
	if response.Message.Content != "" {
		t.Errorf("content = %q, want empty", response.Message.Content)
	}
	if len(response.Message.Thinking) != 1 || response.Message.Thinking[0].Text != "reasoning that never closes" {
		t.Errorf("thinking = %#v", response.Message.Thinking)
	}
}

// TestGenerateStreamReasoningContentAndThinkBlock verifies an endpoint
// that sends both: two thinking blocks, reasoning_content first.
func TestGenerateStreamReasoningContentAndThinkBlock(t *testing.T) {
	t.Parallel()

	chunks := []string{
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"field reasoning\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"<think>tag reasoning</think>answer\"}}]}\n\n",
		"data: [DONE]\n\n",
	}
	server := newSSEServer(t, chunks...)
	client := newClient(t, server.server.URL)

	response, err := client.GenerateStream(context.Background(), userPrompt(), nil)
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}
	if len(response.Message.Thinking) != 2 {
		t.Fatalf("thinking blocks = %#v, want 2", response.Message.Thinking)
	}
	if response.Message.Thinking[0].Text != "field reasoning" || response.Message.Thinking[1].Text != "tag reasoning" {
		t.Errorf("thinking = %#v", response.Message.Thinking)
	}
	if response.Message.Content != "answer" {
		t.Errorf("content = %q", response.Message.Content)
	}
}

// TestFromWireResponseExtractsLeadingThink verifies the non-stream
// DeepSeek-R1-through-a-completion-runtime shape end to end.
func TestFromWireResponseExtractsLeadingThink(t *testing.T) {
	t.Parallel()

	server := newRecordedServer(http.StatusOK, `{
		"choices": [{"message": {
			"role": "assistant",
			"content": "<think>The user asked for a sum. 2 + 2 is 4.</think>2 + 2 equals 4."
		}}],
		"usage": {"prompt_tokens": 9, "completion_tokens": 20}
	}`)
	client := newClient(t, server.server.URL)

	response, err := client.Generate(context.Background(), userPrompt())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if response.Message.Content != "2 + 2 equals 4." {
		t.Errorf("content = %q", response.Message.Content)
	}
	if len(response.Message.Thinking) != 1 || response.Message.Thinking[0].Text != "The user asked for a sum. 2 + 2 is 4." {
		t.Errorf("thinking = %#v", response.Message.Thinking)
	}
}

// TestFromWireResponseKeepsReasoningContentField verifies the
// DeepSeek-API shape: reasoning_content on the message, no tags.
func TestFromWireResponseKeepsReasoningContentField(t *testing.T) {
	t.Parallel()

	server := newRecordedServer(http.StatusOK, `{
		"choices": [{"message": {
			"role": "assistant",
			"reasoning_content": "Let me add.",
			"content": "4"
		}}]
	}`)
	client := newClient(t, server.server.URL)

	response, err := client.Generate(context.Background(), userPrompt())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if response.Message.Content != "4" {
		t.Errorf("content = %q", response.Message.Content)
	}
	if len(response.Message.Thinking) != 1 || response.Message.Thinking[0].Text != "Let me add." {
		t.Errorf("thinking = %#v", response.Message.Thinking)
	}
}
