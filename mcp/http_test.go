package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// streamableServer is a minimal MCP streamable-HTTP server: initialize
// answers JSON with a session header, tools/list answers an SSE stream
// carrying a notification before the response, and tools/call answers
// SSE. Requests record their session headers for assertion.
type streamableServer struct {
	sessionHeader []string
	failCalls     bool
}

func (s *streamableServer) handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var message rpcMessage
	if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	s.sessionHeader = append(s.sessionHeader, r.Header.Get("Mcp-Session-Id"))
	switch message.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "session-1")
		w.Header().Set("Content-Type", "application/json")
		writeJSONResponse(w, message.ID, `{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"http-server","version":"2.0"}}`)
	case "tools/list":
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSEEvent(w, `{"jsonrpc":"2.0","method":"notifications/message"}`)
		writeSSEEvent(w, jsonEncode(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(message.ID),
			"result": map[string]any{
				"tools": []map[string]any{{
					"name":        "echo",
					"description": "Echo a message.",
					"inputSchema": map[string]any{"type": "object"},
				}},
			},
		}))
	case "tools/call":
		if s.failCalls {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSEEvent(w, jsonEncode(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(message.ID),
			"result": map[string]any{
				"content": []map[string]string{{"type": "text", "text": "echo: hi"}},
				"isError": false,
			},
		}))
	default:
		// Notifications and replies to server requests: accepted.
		w.WriteHeader(http.StatusAccepted)
	}
}

func writeJSONResponse(w http.ResponseWriter, id json.RawMessage, result string) {
	fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, id, result)
}

func writeSSEEvent(w http.ResponseWriter, data string) {
	fmt.Fprintf(w, "data: %s\n\n", data)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func TestHTTPValidatesConfig(t *testing.T) {
	cases := []struct {
		cfg  HTTPConfig
		want string
	}{
		{HTTPConfig{}, "url is required"},
		{HTTPConfig{URL: "ftp://example.com/mcp"}, "http or https"},
	}
	for _, testCase := range cases {
		if _, err := NewHTTP(testCase.cfg); err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Errorf("NewHTTP(%+v) error = %v, want containing %q", testCase.cfg, err, testCase.want)
		}
	}
}

func TestHTTPEndToEnd(t *testing.T) {
	server := &streamableServer{}
	httpServer := httptest.NewServer(http.HandlerFunc(server.handler))
	defer httpServer.Close()

	transport, err := NewHTTP(HTTPConfig{URL: httpServer.URL})
	if err != nil {
		t.Fatalf("NewHTTP error = %v", err)
	}
	client := NewClient(transport)
	defer client.Close()
	ctx := context.Background()

	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	if got := client.Server(); got.Name != "http-server" || got.Version != "2.0" {
		t.Errorf("Server() = %+v", got)
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("ListTools = %+v, want one echo tool", tools)
	}

	result, err := client.CallTool(ctx, "echo", json.RawMessage(`{"msg":"hi"}`))
	if err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if result.Text() != "echo: hi" {
		t.Errorf("Text() = %q, want %q", result.Text(), "echo: hi")
	}

	// Every request after initialize must carry the session header;
	// the first (initialize itself) must not. Four POSTs total:
	// initialize, initialized, tools/list, tools/call.
	if len(server.sessionHeader) != 4 {
		t.Fatalf("server saw %d requests, want 4 (initialize, initialized, list, call)", len(server.sessionHeader))
	}
	if server.sessionHeader[0] != "" {
		t.Errorf("initialize carried session %q; the client had none yet", server.sessionHeader[0])
	}
	for i, session := range server.sessionHeader[1:] {
		if session != "session-1" {
			t.Errorf("request %d session = %q, want session-1", i+1, session)
		}
	}
}

func TestHTTPStatusError(t *testing.T) {
	server := &streamableServer{failCalls: true}
	httpServer := httptest.NewServer(http.HandlerFunc(server.handler))
	defer httpServer.Close()

	transport, err := NewHTTP(HTTPConfig{URL: httpServer.URL})
	if err != nil {
		t.Fatalf("NewHTTP error = %v", err)
	}
	client := NewClient(transport)
	defer client.Close()
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	_, err = client.CallTool(context.Background(), "echo", nil)
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("CallTool error = %v, want *HTTPStatusError in the chain", err)
	}
	if statusErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", statusErr.StatusCode)
	}
}

func TestHTTPStreamEndBlocksUntilContext(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		var message rpcMessage
		if json.NewDecoder(r.Body).Decode(&message) != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		switch message.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			writeJSONResponse(w, message.ID, `{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"s","version":"1"}}`)
		default:
			if len(message.ID) > 0 && message.Method == "tools/call" {
				// A stream that closes without ever answering.
				w.Header().Set("Content-Type", "text/event-stream")
				writeSSEEvent(w, `{"jsonrpc":"2.0","method":"notifications/message"}`)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		}
	}
	httpServer := httptest.NewServer(http.HandlerFunc(handler))
	defer httpServer.Close()

	transport, err := NewHTTP(HTTPConfig{URL: httpServer.URL})
	if err != nil {
		t.Fatalf("NewHTTP error = %v", err)
	}
	client := NewClient(transport)
	defer client.Close()
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err = client.CallTool(ctx, "echo", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("CallTool error = %v, want context.DeadlineExceeded in the chain", err)
	}
}
