package anthropic_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/anthropic"
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

func userPrompt() model.Request {
	return model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}
}

func TestGenerateStreamEmitsContentDeltas(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		"event: message_start\n",
		`data: {"type":"message_start","message":{"usage":{"input_tokens":25,"output_tokens":1}}}`+"\n\n",
		"event: content_block_start\n",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`+"\n\n",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`+"\n\n",
		"event: ping\n",
		`data: {"type":"ping"}`+"\n\n",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`+"\n\n",
		`data: {"type":"content_block_stop","index":0}`+"\n\n",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":15}}`+"\n\n",
		`data: {"type":"message_stop"}`+"\n\n",
	)
	client := newClient(t, server.server.URL)

	var fragments []string
	response, err := client.GenerateStream(context.Background(), userPrompt(), func(d model.Delta) error {
		fragments = append(fragments, d.Content)
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}

	if len(fragments) != 2 || fragments[0] != "Hel" || fragments[1] != "lo" {
		t.Fatalf("fragments = %#v, want [Hel lo]", fragments)
	}
	if response.Message.Role != model.RoleAssistant || response.Message.Content != "Hello" {
		t.Fatalf("assembled message = %#v", response.Message)
	}
	// input_tokens comes from message_start; the final output_tokens from
	// the cumulative message_delta.
	if response.Usage != (model.Usage{InputTokens: 25, OutputTokens: 15}) {
		t.Fatalf("usage = %#v", response.Usage)
	}
	if !strings.Contains(server.lastBody(t), `"stream":true`) {
		t.Fatalf("wire body = %s, want stream mode", server.lastBody(t))
	}
}

func TestGenerateStreamAccumulatesToolCalls(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":9,"output_tokens":1}}}`+"\n\n",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`+"\n\n",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Rolling."}}`+"\n\n",
		`data: {"type":"content_block_stop","index":0}`+"\n\n",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu-1","name":"roll","input":{}}}`+"\n\n",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"n"}}`+"\n\n",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\":2}"}}`+"\n\n",
		`data: {"type":"content_block_stop","index":1}`+"\n\n",
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu-2","name":"roll","input":{}}}`+"\n\n",
		`data: {"type":"content_block_stop","index":2}`+"\n\n",
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`+"\n\n",
		`data: {"type":"message_stop"}`+"\n\n",
	)
	client := newClient(t, server.server.URL)

	var toolFragments []model.ToolCallDelta
	response, err := client.GenerateStream(context.Background(), userPrompt(), func(d model.Delta) error {
		toolFragments = append(toolFragments, d.ToolCalls...)
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}

	if response.Message.Content != "Rolling." {
		t.Fatalf("content = %q", response.Message.Content)
	}
	wantCalls := []model.ToolCall{
		{ID: "toolu-1", Name: "roll", Args: []byte(`{"n":2}`)},
		{ID: "toolu-2", Name: "roll", Args: []byte(`{}`)},
	}
	if len(response.Message.ToolCalls) != len(wantCalls) {
		t.Fatalf("tool calls = %#v, want %#v", response.Message.ToolCalls, wantCalls)
	}
	for i, want := range wantCalls {
		got := response.Message.ToolCalls[i]
		if got.ID != want.ID || got.Name != want.Name || string(got.Args) != string(want.Args) {
			t.Fatalf("tool call %d = %#v, want %#v", i, got, want)
		}
	}

	// Fragments correlate with assembled calls by ordinal; ID and Name
	// ride only the first fragment. The empty-input call emits nothing.
	if len(toolFragments) != 2 {
		t.Fatalf("tool fragments = %#v, want 2", toolFragments)
	}
	if toolFragments[0] != (model.ToolCallDelta{Index: 0, ID: "toolu-1", Name: "roll", ArgsFragment: `{"n`}) {
		t.Fatalf("first tool fragment = %#v", toolFragments[0])
	}
	if toolFragments[1] != (model.ToolCallDelta{Index: 0, ArgsFragment: `":2}`}) {
		t.Fatalf("second tool fragment = %#v", toolFragments[1])
	}
	if response.Usage != (model.Usage{InputTokens: 9, OutputTokens: 7}) {
		t.Fatalf("usage = %#v", response.Usage)
	}
}

func TestGenerateStreamRequiresMessageStop(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":25,"output_tokens":1}}}`+"\n\n",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`+"\n\n",
	)
	client := newClient(t, server.server.URL)

	response, err := client.GenerateStream(context.Background(), userPrompt(), nil)
	var decodeError *anthropic.DecodeError
	if !errors.As(err, &decodeError) {
		t.Fatalf("GenerateStream() error = %v, want DecodeError", err)
	}
	if response.Message.Content != "" {
		t.Fatalf("content = %q, want no partial success", response.Message.Content)
	}
}

func TestGenerateStreamClassifiesMidStreamErrors(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":25,"output_tokens":1}}}`+"\n\n",
		`data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`+"\n\n",
	)
	client := newClient(t, server.server.URL)

	_, err := client.GenerateStream(context.Background(), userPrompt(), nil)
	var apiError *anthropic.APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("GenerateStream() error = %v, want APIError", err)
	}
	if apiError.Code != "overloaded_error" || apiError.Message != "Overloaded" {
		t.Fatalf("APIError = {%q %q}", apiError.Code, apiError.Message)
	}
	if !model.IsRetryable(err) {
		t.Fatal("model.IsRetryable(err) = false, want true for overloaded_error")
	}
}

func TestGenerateStreamReturnsCallerStopErrorAsIs(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":25,"output_tokens":1}}}`+"\n\n",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`+"\n\n",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`+"\n\n",
		`data: {"type":"message_stop"}`+"\n\n",
	)
	client := newClient(t, server.server.URL)

	stop := errors.New("caller is done")
	_, err := client.GenerateStream(context.Background(), userPrompt(), func(d model.Delta) error {
		return stop
	})
	if !errors.Is(err, stop) || err.Error() != stop.Error() {
		t.Fatalf("GenerateStream() error = %v, want the caller stop error as-is", err)
	}
}

func TestGenerateStreamPropagatesCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := newSSEServer(t, `data: {"type":"message_stop"}`+"\n\n")
	client := newClient(t, server.server.URL)

	_, err := client.GenerateStream(ctx, userPrompt(), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GenerateStream() error = %v, want context.Canceled", err)
	}
	var transportError *anthropic.TransportError
	if !errors.As(err, &transportError) {
		t.Fatalf("GenerateStream() error = %v, want TransportError", err)
	}
	if model.IsRetryable(err) {
		t.Fatal("model.IsRetryable(err) = true, want false for cancellation")
	}
}
