package azure_test

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
	"github.com/abubakarsiddik31/golem/providers/azure"
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
		`data: {"choices":[{"delta":{"role":"assistant","content":"Hel"}}]}`+"\n\n",
		`data: {"choices":[{"delta":{"content":"lo"}}]}`+"\n\n",
		`data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2}}`+"\n\n",
		"data: [DONE]\n\n",
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
	if response.Message.Content != "Hello" || response.Usage != (model.Usage{InputTokens: 5, OutputTokens: 2}) {
		t.Fatalf("response = %#v", response)
	}
	if body := server.lastBody(t); !strings.Contains(body, `"stream":true`) || !strings.Contains(body, `"include_usage":true`) {
		t.Fatalf("wire body = %s, want streaming with usage", body)
	}
}

func TestGenerateStreamRequiresDoneSentinel(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		`data: {"choices":[{"delta":{"content":"partial"}}]}`+"\n\n",
	)
	client := newClient(t, server.server.URL)

	_, err := client.GenerateStream(context.Background(), userPrompt(), nil)
	var decodeError *azure.DecodeError
	if !errors.As(err, &decodeError) {
		t.Fatalf("GenerateStream() error = %v, want DecodeError for truncation", err)
	}
}

func TestGenerateStreamReturnsCallerStopErrorAsIs(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		`data: {"choices":[{"delta":{"content":"Hel"}}]}`+"\n\n",
		"data: [DONE]\n\n",
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
	server := newSSEServer(t, "data: [DONE]\n\n")
	client := newClient(t, server.server.URL)

	_, err := client.GenerateStream(ctx, userPrompt(), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GenerateStream() error = %v, want context.Canceled", err)
	}
	var transportError *azure.TransportError
	if !errors.As(err, &transportError) {
		t.Fatalf("GenerateStream() error = %v, want TransportError", err)
	}
}
