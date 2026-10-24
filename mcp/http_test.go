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

// streamableServer is a minimal legacy MCP streamable-HTTP server:
// server/discover answers method-not-found, initialize answers JSON with
// a session header, tools/list answers an SSE stream carrying a
// notification before the response, and tools/call answers SSE.
// Requests record their session headers for assertion.
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
	case "server/discover":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":%d,"message":"method not found: server/discover"}}`, message.ID, methodNotFound)
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
	if got := client.ProtocolVersion(); got != legacyProtocolVersion {
		t.Errorf("ProtocolVersion() = %q, want %q", got, legacyProtocolVersion)
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

	// The probe runs first and carries no session; every request after
	// initialize must carry the session header, and initialize itself
	// must not. Five POSTs total: server/discover, initialize,
	// initialized, tools/list, tools/call.
	if len(server.sessionHeader) != 5 {
		t.Fatalf("server saw %d requests, want 5 (discover, initialize, initialized, list, call)", len(server.sessionHeader))
	}
	for i, session := range server.sessionHeader[:2] {
		if session != "" {
			t.Errorf("request %d carried session %q; the client had none yet", i, session)
		}
	}
	for i, session := range server.sessionHeader[2:] {
		if session != "session-1" {
			t.Errorf("request %d session = %q, want session-1", i+2, session)
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
		case "server/discover":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":%d,"message":"method not found"}}`, message.ID, methodNotFound)
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

// modernServer is a minimal stateless MCP streamable-HTTP server: it
// answers server/discover and the tools methods, records every
// request's headers and body, and can reject every request with a fixed
// 400 body.
type modernServer struct {
	headers []http.Header
	bodies  []rpcMessage
	reject  string
}

func (s *modernServer) handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var message rpcMessage
	if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	s.headers = append(s.headers, r.Header.Clone())
	s.bodies = append(s.bodies, message)
	if s.reject != "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, s.reject)
		return
	}
	switch message.Method {
	case "server/discover":
		// A modern server has no session, so this header must be
		// ignored by the client rather than echoed back.
		w.Header().Set("Mcp-Session-Id", "modern-session")
		w.Header().Set("Content-Type", "application/json")
		writeJSONResponse(w, message.ID, `{"resultType":"complete","supportedVersions":["2026-07-28"],"capabilities":{"tools":{}},"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"modern-http","version":"3.0"}}}`)
	case "tools/list":
		w.Header().Set("Content-Type", "application/json")
		writeJSONResponse(w, message.ID, `{"resultType":"complete","tools":[{"name":"echo","inputSchema":{"type":"object"}}]}`)
	case "tools/call":
		w.Header().Set("Content-Type", "application/json")
		writeJSONResponse(w, message.ID, `{"resultType":"complete","content":[{"type":"text","text":"hi"}],"isError":false}`)
	default:
		http.Error(w, "unknown method", http.StatusNotFound)
	}
}

func TestHTTPModernEra(t *testing.T) {
	server := &modernServer{}
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
	if got := client.ProtocolVersion(); got != modernProtocolVersion {
		t.Errorf("ProtocolVersion() = %q, want %q", got, modernProtocolVersion)
	}
	if got := client.Server(); got.Name != "modern-http" || got.Version != "3.0" {
		t.Errorf("Server() = %+v, want the _meta identity", got)
	}
	if _, err := client.CallTool(ctx, "echo", json.RawMessage(`{"msg":"hi"}`)); err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if _, err := client.CallTool(ctx, "héllo", nil); err != nil {
		t.Fatalf("CallTool error = %v", err)
	}

	if len(server.headers) != 3 {
		t.Fatalf("server saw %d requests, want discover and two calls", len(server.headers))
	}
	wantMethods := []string{"server/discover", "tools/call", "tools/call"}
	for i, header := range server.headers {
		if got := header.Get("MCP-Protocol-Version"); got != modernProtocolVersion {
			t.Errorf("request %d MCP-Protocol-Version = %q, want %q", i, got, modernProtocolVersion)
		}
		if got := header.Get("Mcp-Method"); got != wantMethods[i] {
			t.Errorf("request %d Mcp-Method = %q, want %q", i, got, wantMethods[i])
		}
		if got := header.Get("Mcp-Session-Id"); got != "" {
			t.Errorf("request %d carried session %q in a stateless session", i, got)
		}
	}
	if got := server.headers[0].Get("Mcp-Name"); got != "" {
		t.Errorf("discovery Mcp-Name = %q, want empty", got)
	}
	if got := server.headers[1].Get("Mcp-Name"); got != "echo" {
		t.Errorf("tools/call Mcp-Name = %q, want echo", got)
	}
	if got, want := server.headers[2].Get("Mcp-Name"), "=?base64?aMOpbGxv?="; got != want {
		t.Errorf("non-ASCII Mcp-Name = %q, want %q", got, want)
	}

	var probe struct {
		Meta struct {
			ProtocolVersion string `json:"io.modelcontextprotocol/protocolVersion"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(server.bodies[0].Params, &probe); err != nil {
		t.Fatalf("discovery params: %v", err)
	}
	if probe.Meta.ProtocolVersion != modernProtocolVersion {
		t.Errorf("discovery _meta protocolVersion = %q, want %q", probe.Meta.ProtocolVersion, modernProtocolVersion)
	}
}

func TestHTTPUnsupportedProtocolVersionBody(t *testing.T) {
	server := &modernServer{reject: `{"jsonrpc":"2.0","id":1,"error":{"code":-32022,"message":"unsupported protocol version","data":{"requested":"2026-07-28","supported":["2025-06-18"]}}}`}
	httpServer := httptest.NewServer(http.HandlerFunc(server.handler))
	defer httpServer.Close()

	transport, err := NewHTTP(HTTPConfig{URL: httpServer.URL})
	if err != nil {
		t.Fatalf("NewHTTP error = %v", err)
	}
	client := NewClient(transport)
	defer client.Close()

	err = client.Initialize(context.Background())
	var unsupported *UnsupportedProtocolVersionError
	if !errors.As(err, &unsupported) {
		t.Fatalf("Initialize error = %v, want *UnsupportedProtocolVersionError", err)
	}
	if len(unsupported.Supported) != 1 || unsupported.Supported[0] != "2025-06-18" {
		t.Errorf("Supported = %v, want [2025-06-18]", unsupported.Supported)
	}
	if len(server.headers) != 1 {
		t.Errorf("server saw %d requests, want no legacy fallback after a modern rejection", len(server.headers))
	}
}

func TestHTTPProbeBadRequestFallsBackToLegacy(t *testing.T) {
	var methods []string
	handler := func(w http.ResponseWriter, r *http.Request) {
		var message rpcMessage
		if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		methods = append(methods, message.Method)
		switch message.Method {
		case "server/discover":
			// A legacy endpoint may reject an unknown method outright.
			http.Error(w, "unknown method", http.StatusBadRequest)
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			writeJSONResponse(w, message.ID, `{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"legacy-http","version":"1.0"}}`)
		default:
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
	if got := client.ProtocolVersion(); got != legacyProtocolVersion {
		t.Errorf("ProtocolVersion() = %q, want %q", got, legacyProtocolVersion)
	}
	if len(methods) < 2 || methods[0] != "server/discover" || methods[1] != "initialize" {
		t.Errorf("request methods = %v, want the probe then initialize", methods)
	}
}

func TestEncodeHeaderValue(t *testing.T) {
	cases := []struct {
		value string
		want  string
	}{
		{"echo", "echo"},
		{"us-west1", "us-west1"},
		{" padded ", "=?base64?IHBhZGRlZCA=?="},
		{"Hello, 世界", "=?base64?SGVsbG8sIOS4lueVjA==?="},
		{"=?base64?literal?=", "=?base64?PT9iYXNlNjQ/bGl0ZXJhbD89?="},
		{"", "=?base64??="},
	}
	for _, testCase := range cases {
		if got := encodeHeaderValue(testCase.value); got != testCase.want {
			t.Errorf("encodeHeaderValue(%q) = %q, want %q", testCase.value, got, testCase.want)
		}
	}
}

func TestParseRequestMetadata(t *testing.T) {
	cases := []struct {
		name    string
		message string
		want    requestMetadata
	}{
		{
			name:    "legacy request",
			message: `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
			want:    requestMetadata{method: "tools/list"},
		},
		{
			name:    "modern tools call",
			message: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
			want:    requestMetadata{version: modernProtocolVersion, method: "tools/call", name: "echo"},
		},
		{
			name:    "modern resources read",
			message: `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"file:///a","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
			want:    requestMetadata{version: modernProtocolVersion, method: "resources/read", name: "file:///a"},
		},
		{
			name:    "modern prompts get",
			message: `{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"summarize","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
			want:    requestMetadata{version: modernProtocolVersion, method: "prompts/get", name: "summarize"},
		},
		{
			name:    "malformed body",
			message: `not json`,
			want:    requestMetadata{},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := parseRequestMetadata([]byte(testCase.message)); got != testCase.want {
				t.Errorf("parseRequestMetadata = %+v, want %+v", got, testCase.want)
			}
		})
	}
}
