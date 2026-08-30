package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/openai"
)

// sseServer streams raw chunks with a flush after each and records the
// last request body.
type sseServer struct {
	server *httptest.Server

	mu     sync.Mutex
	bodies []string
}

func newSSEServer(t *testing.T, chunks ...string) *sseServer {
	t.Helper()
	s := &sseServer{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies = append(s.bodies, string(payload))
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, chunk := range chunks {
			_, _ = w.Write([]byte(chunk))
			flusher.Flush()
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *sseServer) lastBody(t *testing.T) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) == 0 {
		t.Fatal("sseServer received no requests")
	}
	return s.bodies[len(s.bodies)-1]
}

func TestGenerateStreamEmitsContentDeltas(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		"event: greeting\n\n",
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Hel\"}}]}\n\n",
		": keep-alive\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n",
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\n",
		"data: [DONE]\n\n",
	)
	client := newClient(t, server.server.URL)

	var fragments []string
	response, err := client.GenerateStream(context.Background(), userPrompt(), func(d model.Delta) error {
		if d.Content != "" {
			fragments = append(fragments, d.Content)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}
	if strings.Join(fragments, "") != "Hello" || len(fragments) != 2 {
		t.Fatalf("fragments = %#v, want [Hel lo]", fragments)
	}
	if response.Message.Role != model.RoleAssistant || response.Message.Content != "Hello" {
		t.Fatalf("assembled message = %#v", response.Message)
	}
	if response.Usage != (model.Usage{InputTokens: 5, OutputTokens: 2}) {
		t.Fatalf("usage = %#v, want the final chunk's usage", response.Usage)
	}

	// The request must select streaming and request streamed usage.
	var sent struct {
		Stream        bool `json:"stream"`
		StreamOptions *struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if err := json.Unmarshal([]byte(server.lastBody(t)), &sent); err != nil {
		t.Fatalf("decode sent request: %v", err)
	}
	if !sent.Stream || sent.StreamOptions == nil || !sent.StreamOptions.IncludeUsage {
		t.Fatalf("sent request = %s, want stream and include_usage", server.lastBody(t))
	}
}

func TestGenerateStreamAccumulatesToolCalls(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"get_player_name\"}}]}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"player_id\\\":\"}}]}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"7}\"}}]}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"call-2\",\"type\":\"function\",\"function\":{\"name\":\"reset\"}}]}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"function\":{\"arguments\":\"\"}}]}}]}\n\n",
		"data: [DONE]\n\n",
	)
	client := newClient(t, server.server.URL)

	var deltas []model.Delta
	response, err := client.GenerateStream(context.Background(), userPrompt(), func(d model.Delta) error {
		deltas = append(deltas, d)
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}

	// Fragment identity: the first fragment of each call carries ID and
	// Name; argument fragments carry only their piece.
	if len(deltas) != 5 {
		t.Fatalf("deltas = %#v, want 5", deltas)
	}
	first := deltas[0].ToolCalls[0]
	if first.ID != "call-1" || first.Name != "get_player_name" || first.ArgsFragment != "" {
		t.Fatalf("first fragment = %#v, want identification only", first)
	}
	if deltas[1].ToolCalls[0].ID != "" || deltas[1].ToolCalls[0].ArgsFragment != `{"player_id":` {
		t.Fatalf("argument fragment = %#v", deltas[1].ToolCalls[0])
	}
	// The trailing empty-argument fragment still reports progress on its
	// call, identifying nothing.
	last := deltas[4].ToolCalls[0]
	if last.Index != 1 || last.ID != "" || last.Name != "" || last.ArgsFragment != "" {
		t.Fatalf("trailing fragment = %#v, want empty fragment for index 1", last)
	}

	calls := response.Message.ToolCalls
	if len(calls) != 2 {
		t.Fatalf("assembled calls = %#v", calls)
	}
	if calls[0].ID != "call-1" || calls[0].Name != "get_player_name" || string(calls[0].Args) != `{"player_id":7}` {
		t.Fatalf("first call = %#v", calls[0])
	}
	if calls[1].ID != "call-2" || calls[1].Name != "reset" || string(calls[1].Args) != "{}" {
		t.Fatalf("second call = %#v, want empty arguments normalized to {}", calls[1])
	}
}

func TestGenerateStreamAcceptsCompleteToolCallsInSingleChunks(t *testing.T) {
	t.Parallel()

	// Local OpenAI-compatible servers (Ollama, LM Studio) deliver each
	// tool call whole — ID, name, and full arguments in one chunk —
	// instead of the fragmented accumulation shape.
	server := newSSEServer(t,
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"part\\\":\\\"memory\\\"}\"}}]}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"call-2\",\"type\":\"function\",\"function\":{\"name\":\"reset\",\"arguments\":\"{}\"}}]}}]}\n\n",
		"data: [DONE]\n\n",
	)
	client := newClient(t, server.server.URL)

	var deltas []model.Delta
	response, err := client.GenerateStream(context.Background(), userPrompt(), func(d model.Delta) error {
		deltas = append(deltas, d)
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}

	if len(deltas) != 2 {
		t.Fatalf("deltas = %#v, want one per complete call", deltas)
	}
	first := deltas[0].ToolCalls[0]
	if first.ID != "call-1" || first.Name != "lookup" || first.ArgsFragment != `{"part":"memory"}` {
		t.Fatalf("first fragment = %#v, want the complete call", first)
	}

	calls := response.Message.ToolCalls
	if len(calls) != 2 {
		t.Fatalf("assembled calls = %#v, want 2", calls)
	}
	if calls[0].ID != "call-1" || calls[0].Name != "lookup" || string(calls[0].Args) != `{"part":"memory"}` {
		t.Fatalf("first call = %#v", calls[0])
	}
	if calls[1].ID != "call-2" || calls[1].Name != "reset" || string(calls[1].Args) != "{}" {
		t.Fatalf("second call = %#v", calls[1])
	}
}

func TestGenerateStreamRequiresDoneSentinel(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		"data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n",
	)
	client := newClient(t, server.server.URL)

	var fragments []string
	_, err := client.GenerateStream(context.Background(), userPrompt(), func(d model.Delta) error {
		if d.Content != "" {
			fragments = append(fragments, d.Content)
		}
		return nil
	})
	var decodeErr *openai.DecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("GenerateStream() error = %v, want DecodeError", err)
	}
	if !strings.Contains(err.Error(), "[DONE]") {
		t.Fatalf("GenerateStream() error = %v, want missing-sentinel message", err)
	}
	if len(fragments) != 1 {
		t.Fatalf("fragments = %#v, want the one emitted before truncation", fragments)
	}
}

func TestGenerateStreamClassifiesProviderErrors(t *testing.T) {
	t.Parallel()

	server := newRecordedServer(http.StatusBadGateway,
		`{"error":{"code":"server_error","message":"upstream down"}}`)
	client := newClient(t, server.server.URL)

	_, err := client.GenerateStream(context.Background(), userPrompt(), func(model.Delta) error {
		return nil
	})
	var apiErr *openai.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("GenerateStream() error = %v, want 502 APIError", err)
	}
	if !model.IsRetryable(err) {
		t.Fatalf("GenerateStream() error = %v, want retryable 5xx", err)
	}
}

func TestGenerateStreamReturnsCallerStopErrorAsIs(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		"data: {\"choices\":[{\"delta\":{\"content\":\"enough\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"more\"}}]}\n\n",
		"data: [DONE]\n\n",
	)
	client := newClient(t, server.server.URL)

	stop := errors.New("caller is done listening")
	var fragments int
	_, err := client.GenerateStream(context.Background(), userPrompt(), func(d model.Delta) error {
		fragments++
		return stop
	})
	if !errors.Is(err, stop) || err != stop {
		t.Fatalf("GenerateStream() error = %v, want the caller's stop error as-is", err)
	}
	var transportErr *openai.TransportError
	var apiErr *openai.APIError
	var decodeErr *openai.DecodeError
	if errors.As(err, &transportErr) || errors.As(err, &apiErr) || errors.As(err, &decodeErr) {
		t.Fatalf("GenerateStream() error = %v, stop must not be wrapped in a provider error", err)
	}
	if fragments != 1 {
		t.Fatalf("fragments = %d, want streaming to stop after the first", fragments)
	}
}

func TestGenerateStreamPropagatesCancellation(t *testing.T) {
	t.Parallel()

	// The handler streams one fragment, then holds the body open until
	// the request context closes, so the next read fails deterministically.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n"))
		flusher.Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	client := newClient(t, server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	var fragments int
	_, err := client.GenerateStream(ctx, userPrompt(), func(d model.Delta) error {
		fragments++
		cancel()
		return nil
	})
	var transportErr *openai.TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("GenerateStream() error = %v, want TransportError", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GenerateStream() error = %v, want context.Canceled in the chain", err)
	}
	if model.IsRetryable(err) {
		t.Fatalf("GenerateStream() error = %v, cancellation must not be retryable", err)
	}
	if fragments != 1 {
		t.Fatalf("fragments = %d, want 1 before cancellation", fragments)
	}
}

func TestGenerateStreamCapturesReasoningDeltas(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		`data: {"choices":[{"delta":{"reasoning_content":"adding"}}]}`+"\n\n",
		`data: {"choices":[{"delta":{"reasoning_content":" 2 and 2"}}]}`+"\n\n",
		`data: {"choices":[{"delta":{"content":"4"}}]}`+"\n\n",
		`data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2}}`+"\n\n",
		"data: [DONE]\n\n",
	)
	client := newClient(t, server.server.URL)

	var thinking []model.ThinkingDelta
	var fragments []string
	response, err := client.GenerateStream(context.Background(), userPrompt(), func(d model.Delta) error {
		thinking = append(thinking, d.Thinking...)
		if d.Content != "" {
			fragments = append(fragments, d.Content)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}

	// Reasoning precedes the answer, and only the content fragment is
	// non-empty in its own delta.
	if len(thinking) != 2 || thinking[0].Text != "adding" || thinking[1].Text != " 2 and 2" {
		t.Fatalf("thinking deltas = %#v", thinking)
	}
	if len(fragments) != 1 || fragments[0] != "4" {
		t.Fatalf("content fragments = %#v", fragments)
	}
	thinkingBlocks := response.Message.Thinking
	if len(thinkingBlocks) != 1 || thinkingBlocks[0].Text != "adding 2 and 2" {
		t.Fatalf("assembled thinking = %#v", thinkingBlocks)
	}
	if response.Message.Content != "4" {
		t.Fatalf("assembled content = %q", response.Message.Content)
	}
}
