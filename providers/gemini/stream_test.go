package gemini_test

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
	"github.com/abubakarsiddik31/golem/providers/gemini"
)

// sseServer streams raw chunks with a flush after each and records the
// last request path and body.
type sseServer struct {
	server *httptest.Server

	mu     sync.Mutex
	bodies []string
	paths  []string
}

func newSSEServer(t *testing.T, chunks ...string) *sseServer {
	t.Helper()
	s := &sseServer{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies = append(s.bodies, string(payload))
		s.paths = append(s.paths, r.URL.Path)
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

func (s *sseServer) last(t *testing.T) (string, string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) == 0 {
		t.Fatal("sseServer received no requests")
	}
	return s.bodies[len(s.bodies)-1], s.paths[len(s.paths)-1]
}

func userPrompt() model.Request {
	return model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}
}

func TestGenerateStreamEmitsContentDeltas(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hel"}]}}],"usageMetadata":{"promptTokenCount":25,"candidatesTokenCount":1}}`+"\n\n",
		": keep-alive\n\n",
		`data: {"candidates":[{"content":{"parts":[{"text":"lo"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":25,"candidatesTokenCount":15}}`+"\n\n",
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
	if response.Message.Content != "Hello" {
		t.Fatalf("assembled content = %q", response.Message.Content)
	}
	// The last chunk's cumulative usageMetadata wins.
	if response.Usage != (model.Usage{InputTokens: 25, OutputTokens: 15}) {
		t.Fatalf("usage = %#v", response.Usage)
	}

	body, path := server.last(t)
	if !strings.HasSuffix(path, ":streamGenerateContent") {
		t.Fatalf("path = %q, want the streamGenerateContent endpoint", path)
	}
	if !strings.Contains(body, `"contents"`) {
		t.Fatalf("wire body = %s, want contents", body)
	}
}

func TestGenerateStreamCarriesFunctionCalls(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		`data: {"candidates":[{"content":{"parts":[{"text":"Rolling."}]}}]}`+"\n\n",
		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"roll","args":{"n":4}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":7}}`+"\n\n",
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
	want := model.ToolCall{ID: "call-1", Name: "roll", Args: []byte(`{"n":4}`)}
	if len(response.Message.ToolCalls) != 1 ||
		response.Message.ToolCalls[0].ID != want.ID ||
		response.Message.ToolCalls[0].Name != want.Name ||
		string(response.Message.ToolCalls[0].Args) != string(want.Args) {
		t.Fatalf("tool calls = %#v, want %#v", response.Message.ToolCalls, want)
	}
	// Function calls arrive whole: one fragment with ID, Name, and the
	// complete arguments.
	if len(toolFragments) != 1 || toolFragments[0] != (model.ToolCallDelta{
		Index: 0, ID: "call-1", Name: "roll", ArgsFragment: `{"n":4}`,
	}) {
		t.Fatalf("tool fragments = %#v", toolFragments)
	}
	if response.Usage != (model.Usage{InputTokens: 9, OutputTokens: 7}) {
		t.Fatalf("usage = %#v", response.Usage)
	}
}

func TestGenerateStreamReturnsCallerStopErrorAsIs(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		`data: {"candidates":[{"content":{"parts":[{"text":"Hel"}]}}]}`+"\n\n",
		`data: {"candidates":[{"content":{"parts":[{"text":"lo"}]}}]}`+"\n\n",
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

// A stream that ends without a finishReason-bearing chunk was truncated
// in transit; it must fail rather than pass as a short complete answer.
func TestGenerateStreamFailsStreamEndingWithoutFinishReason(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"partial an"}]}}],"usageMetadata":{"promptTokenCount":25,"candidatesTokenCount":3}}`+"\n\n",
		`data: {"candidates":[{"content":{"parts":[{"text":"swer"}]}}],"usageMetadata":{"promptTokenCount":25,"candidatesTokenCount":9}}`+"\n\n",
	)
	client := newClient(t, server.server.URL)

	response, err := client.GenerateStream(context.Background(), userPrompt(), nil)
	if err == nil {
		t.Fatalf("GenerateStream() = %+v, want an error for a stream with no terminal finishReason", response)
	}
	var decodeError *gemini.DecodeError
	if !errors.As(err, &decodeError) {
		t.Fatalf("GenerateStream() error = %v, want DecodeError", err)
	}
	if model.IsRetryable(err) {
		t.Fatal("model.IsRetryable(err) = true, want false for a truncated stream")
	}
}

// The terminal finishReason may share its chunk with content; any chunk
// carrying one satisfies the terminal requirement, including non-STOP
// reasons such as MAX_TOKENS.
func TestGenerateStreamAcceptsNonStopFinishReason(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		`data: {"candidates":[{"content":{"parts":[{"text":"cut sh"}]}}]}`+"\n\n",
		`data: {"candidates":[{"content":{"parts":[{"text":"ort"}]},"finishReason":"MAX_TOKENS"}],"usageMetadata":{"promptTokenCount":25,"candidatesTokenCount":9}}`+"\n\n",
	)
	client := newClient(t, server.server.URL)

	response, err := client.GenerateStream(context.Background(), userPrompt(), nil)
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}
	if response.Message.Content != "cut short" {
		t.Fatalf("assembled content = %q, want %q", response.Message.Content, "cut short")
	}
}

func TestGenerateStreamPropagatesCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := newSSEServer(t, `data: {}`+"\n\n")
	client := newClient(t, server.server.URL)

	_, err := client.GenerateStream(ctx, userPrompt(), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GenerateStream() error = %v, want context.Canceled", err)
	}
	var transportError *gemini.TransportError
	if !errors.As(err, &transportError) {
		t.Fatalf("GenerateStream() error = %v, want TransportError", err)
	}
	if model.IsRetryable(err) {
		t.Fatal("model.IsRetryable(err) = true, want false for cancellation")
	}
}

func TestGenerateStreamAssemblesThoughtParts(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		// Two contiguous thought parts join into one block; the signature
		// rides the fragment that carries it.
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"ponder","thought":true}]}}]}`+"\n\n",
		`data: {"candidates":[{"content":{"parts":[{"text":"ing","thought":true,"thoughtSignature":"sig-1"}]}}]}`+"\n\n",
		// The function call ends the thinking block and carries its own
		// signature for the next turn.
		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"calc","args":{"op":"add"}},"thoughtSignature":"callsig"}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":6}}`+"\n\n",
		`data: {"candidates":[{"content":{"parts":[{"text":"4"}]},"finishReason":"STOP"}]}`+"\n\n",
	)
	client := newClient(t, server.server.URL)

	var thinking []model.ThinkingDelta
	var calls []model.ToolCallDelta
	response, err := client.GenerateStream(context.Background(), userPrompt(), func(d model.Delta) error {
		thinking = append(thinking, d.Thinking...)
		calls = append(calls, d.ToolCalls...)
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}

	if len(thinking) != 2 || thinking[0].Text != "ponder" || thinking[1].Text != "ing" ||
		thinking[1].Signature != "sig-1" || thinking[0].Index != 0 || thinking[1].Index != 0 {
		t.Fatalf("thinking deltas = %#v, want two fragments of block 0", thinking)
	}
	if len(calls) != 1 || calls[0].Signature != "callsig" {
		t.Fatalf("call deltas = %#v, want the thought signature carried", calls)
	}
	thinkingBlocks := response.Message.Thinking
	if len(thinkingBlocks) != 1 || thinkingBlocks[0].Text != "pondering" || thinkingBlocks[0].Signature != "sig-1" {
		t.Fatalf("assembled thinking = %#v", thinkingBlocks)
	}
	if len(response.Message.ToolCalls) != 1 || response.Message.ToolCalls[0].Signature != "callsig" {
		t.Fatalf("assembled calls = %#v", response.Message.ToolCalls)
	}
	if response.Message.Content != "4" {
		t.Fatalf("assembled content = %q", response.Message.Content)
	}
}
