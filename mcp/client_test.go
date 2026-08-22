package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
)

// funcTransport adapts a handler function to Transport: every sent
// message is recorded and the handler's replies are queued for Read.
// An empty queue blocks until the context is done — how tests exercise
// cancellation.
type funcTransport struct {
	handle  func(sent []byte) [][]byte
	sent    [][]byte
	pending [][]byte
}

func (f *funcTransport) Send(_ context.Context, message []byte) error {
	f.sent = append(f.sent, append([]byte(nil), message...))
	if f.handle != nil {
		f.pending = append(f.pending, f.handle(message)...)
	}
	return nil
}

func (f *funcTransport) Read(ctx context.Context) ([]byte, error) {
	if len(f.pending) == 0 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	next := f.pending[0]
	f.pending = f.pending[1:]
	return next, nil
}

func (f *funcTransport) Close() error { return nil }

// decodeSent parses the n-th sent message for assertions.
func decodeSent(t *testing.T, transport *funcTransport, n int) rpcMessage {
	t.Helper()
	if n >= len(transport.sent) {
		t.Fatalf("sent has %d messages, want index %d", len(transport.sent), n)
	}
	var message rpcMessage
	if err := json.Unmarshal(transport.sent[n], &message); err != nil {
		t.Fatalf("sent[%d] is not valid JSON-RPC: %v", n, err)
	}
	return message
}

// serverReply builds a response to id carrying result; a malformed
// result panics so test fixtures fail loudly at the source.
func serverReply(id json.RawMessage, result string) []byte {
	encoded, err := json.Marshal(rpcMessage{JSONRPC: "2.0", ID: id, Result: json.RawMessage(result)})
	if err != nil {
		panic(fmt.Sprintf("serverReply: result is not valid JSON: %v", err))
	}
	return encoded
}

// initializeHandler answers initialize and drops notifications.
func initializeHandler(t *testing.T, extra func(method string, id json.RawMessage, params json.RawMessage) [][]byte) func([]byte) [][]byte {
	return func(sent []byte) [][]byte {
		var message rpcMessage
		if err := json.Unmarshal(sent, &message); err != nil {
			t.Errorf("sent message is not valid JSON-RPC: %v", err)
			return nil
		}
		if message.Method == "" {
			return nil // a response the client sent (e.g. a ping reply)
		}
		if len(message.ID) == 0 {
			return nil // a notification
		}
		if message.Method == "initialize" {
			return [][]byte{serverReply(message.ID, `{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"test-server","version":"1.0"}}`)}
		}
		if extra != nil {
			return extra(message.Method, message.ID, message.Params)
		}
		return nil
	}
}

func TestInitializeHandshake(t *testing.T) {
	transport := &funcTransport{handle: initializeHandler(t, nil)}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}

	request := decodeSent(t, transport, 0)
	if request.Method != "initialize" {
		t.Fatalf("first message method = %q, want initialize", request.Method)
	}
	var params struct {
		ProtocolVersion string     `json:"protocolVersion"`
		ClientInfo      ServerInfo `json:"clientInfo"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatalf("initialize params: %v", err)
	}
	if params.ProtocolVersion != protocolVersion || params.ClientInfo.Name != "golem" {
		t.Errorf("initialize params = %+v", params)
	}
	initialized := decodeSent(t, transport, 1)
	if initialized.Method != "notifications/initialized" || len(initialized.ID) > 0 {
		t.Errorf("second message = %+v, want an initialized notification", initialized)
	}
	if got := client.Server(); got.Name != "test-server" || got.Version != "1.0" {
		t.Errorf("Server() = %+v", got)
	}

	// A second Initialize is a no-op.
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("second Initialize error = %v", err)
	}
	if len(transport.sent) != 2 {
		t.Errorf("sent %d messages after re-initialize, want 2", len(transport.sent))
	}
}

func TestOperationsRequireInitialization(t *testing.T) {
	client := NewClient(&funcTransport{})
	if _, err := client.ListTools(context.Background()); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("ListTools error = %v, want not-initialized", err)
	}
	if _, err := client.CallTool(context.Background(), "echo", nil); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("CallTool error = %v, want not-initialized", err)
	}
}

func TestInitializeProtocolError(t *testing.T) {
	transport := &funcTransport{handle: func(sent []byte) [][]byte {
		var message rpcMessage
		if err := json.Unmarshal(sent, &message); err != nil || message.Method != "initialize" || len(message.ID) == 0 {
			return nil
		}
		encoded, _ := json.Marshal(rpcMessage{
			JSONRPC: "2.0",
			ID:      message.ID,
			Error:   &rpcError{Code: -32602, Message: "invalid params"},
		})
		return [][]byte{encoded}
	}}
	if err := NewClient(transport).Initialize(context.Background()); err == nil {
		t.Fatal("Initialize should fail on an error response")
	} else {
		var protocolErr *ProtocolError
		if !errors.As(err, &protocolErr) || protocolErr.Code != -32602 {
			t.Errorf("Initialize error = %v, want *ProtocolError code -32602", err)
		}
	}
}

func TestListToolsPaginates(t *testing.T) {
	var calls int
	transport := &funcTransport{handle: initializeHandler(t, func(method string, id json.RawMessage, params json.RawMessage) [][]byte {
		if method != "tools/list" {
			t.Errorf("unexpected method %q", method)
			return nil
		}
		calls++
		var request struct {
			Cursor string `json:"cursor"`
		}
		if err := json.Unmarshal(params, &request); err != nil {
			t.Fatalf("tools/list params: %v", err)
		}
		switch {
		case calls == 1 && request.Cursor == "":
			return [][]byte{serverReply(id, `{"tools":[{"name":"a"},{"name":"b"}],"nextCursor":"c1"}`)}
		case calls == 2 && request.Cursor == "c1":
			return [][]byte{serverReply(id, `{"tools":[{"name":"c"}]}`)}
		}
		t.Errorf("tools/list call %d with cursor %q", calls, request.Cursor)
		return nil
	})}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools error = %v", err)
	}
	if len(tools) != 3 || tools[0].Name != "a" || tools[2].Name != "c" {
		t.Errorf("ListTools = %+v, want a, b, c across two pages", tools)
	}
}

func TestCallToolAnswersServerPing(t *testing.T) {
	transport := &funcTransport{handle: initializeHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		// The server pings before answering; the client must reply and
		// keep waiting for its response.
		ping, _ := json.Marshal(rpcMessage{JSONRPC: "2.0", ID: json.RawMessage("900"), Method: "ping"})
		return [][]byte{ping, serverReply(id, `{"content":[{"type":"text","text":"hi"}],"isError":false}`)}
	})}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	result, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{"msg":"x"}`))
	if err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if result.Text() != "hi" {
		t.Errorf("Text() = %q, want %q", result.Text(), "hi")
	}
	var pingReply *rpcMessage
	for i := range transport.sent {
		message := decodeSent(t, transport, i)
		if string(message.ID) == "900" {
			pingReply = &message
		}
	}
	if pingReply == nil {
		t.Fatal("client never replied to the server ping")
	}
	if pingReply.Method != "" || pingReply.Error != nil || canonicalJSON(pingReply.Result) != "{}" {
		t.Errorf("ping reply = %+v, want a result-only response", pingReply)
	}
}

func TestCallToolRejectsServerRequests(t *testing.T) {
	transport := &funcTransport{handle: initializeHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		request, _ := json.Marshal(rpcMessage{JSONRPC: "2.0", ID: json.RawMessage("7"), Method: "roots/list"})
		return [][]byte{request, serverReply(id, `{"content":[],"isError":false}`)}
	})}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	if _, err := client.CallTool(context.Background(), "echo", nil); err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	for i := range transport.sent {
		message := decodeSent(t, transport, i)
		if string(message.ID) == "7" {
			if message.Error == nil || message.Error.Code != methodNotFound {
				t.Errorf("reply to roots/list = %+v, want method-not-found", message)
			}
			return
		}
	}
	t.Fatal("client never replied to the server request")
}

func TestCallResultText(t *testing.T) {
	result := CallResult{Content: []ContentItem{
		{Type: "text", Text: "first"},
		{Type: "image"},
		{Type: "text", Text: "second"},
	}}
	want := "first\n[mcp: unsupported image content]\nsecond"
	if got := result.Text(); got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}
}

func TestCancellationPropagates(t *testing.T) {
	transport := &funcTransport{handle: initializeHandler(t, nil)}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go cancel()
	_, err := client.CallTool(ctx, "echo", nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("CallTool error = %v, want context.Canceled in the chain", err)
	}
}

func toolListReply(tools string) func(string, json.RawMessage, json.RawMessage) [][]byte {
	return func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/list" {
			return nil
		}
		return [][]byte{serverReply(id, tools)}
	}
}

func TestAsToolsBridgesServerTools(t *testing.T) {
	const schema = `{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}`
	transport := &funcTransport{handle: initializeHandler(t, toolListReply(fmt.Sprintf(
		`{"tools":[{"name":"echo","description":"Echo a message.","inputSchema":%s},{"name":"bare"}]}`, schema)))}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}

	tools, err := AsTools[struct{}](context.Background(), client)
	if err != nil {
		t.Fatalf("AsTools error = %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("AsTools returned %d tools, want 2", len(tools))
	}
	echo, bare := tools[0], tools[1]
	if echo.Name != "echo" || echo.Description != "Echo a message." {
		t.Errorf("bridged echo = %q %q", echo.Name, echo.Description)
	}
	if canonicalJSON(echo.Schema) != canonicalJSON(json.RawMessage(schema)) {
		t.Errorf("echo schema = %s, want the server's verbatim", echo.Schema)
	}
	if canonicalJSON(bare.Schema) != canonicalJSON(json.RawMessage(`{"type":"object"}`)) {
		t.Errorf("schema-less tool schema = %s, want an empty object schema", bare.Schema)
	}

	// Executing the tool calls tools/call with the model's raw args.
	transport.handle = initializeHandler(t, func(method string, id json.RawMessage, params json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(params, &call); err != nil {
			t.Fatalf("tools/call params: %v", err)
		}
		if call.Name != "echo" || canonicalJSON(call.Arguments) != canonicalJSON(json.RawMessage(`{"msg":"hi"}`)) {
			t.Errorf("tools/call = %s %s, want echo with the raw arguments", call.Name, call.Arguments)
		}
		return [][]byte{serverReply(id, `{"content":[{"type":"text","text":"echo: hi"}],"isError":false}`)}
	})
	result, err := echo.Exec(context.Background(), struct{}{}, json.RawMessage(`{"msg":"hi"}`))
	if err != nil {
		t.Fatalf("Exec error = %v", err)
	}
	if result != "echo: hi" {
		t.Errorf("Exec result = %q, want %q", result, "echo: hi")
	}
}

func TestAsToolsIsErrorIsCorrectable(t *testing.T) {
	transport := &funcTransport{handle: initializeHandler(t, toolListReply(
		`{"tools":[{"name":"echo","inputSchema":{"type":"object"}}]}`))}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	tools, err := AsTools[struct{}](context.Background(), client)
	if err != nil {
		t.Fatalf("AsTools error = %v", err)
	}

	transport.handle = initializeHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		return [][]byte{serverReply(id, `{"content":[{"type":"text","text":"msg is required"}],"isError":true}`)}
	})
	_, err = tools[0].Exec(context.Background(), struct{}{}, json.RawMessage(`{}`))
	var retry *model.ModelRetry
	if !errors.As(err, &retry) {
		t.Fatalf("Exec error = %v, want *model.ModelRetry in the chain", err)
	}
	if !strings.Contains(retry.Err.Error(), "msg is required") {
		t.Errorf("rejection reason = %v, want the server's text", retry.Err)
	}
}

func TestAsToolsProtocolErrorNotCorrectable(t *testing.T) {
	transport := &funcTransport{handle: initializeHandler(t, toolListReply(
		`{"tools":[{"name":"echo","inputSchema":{"type":"object"}}]}`))}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	tools, err := AsTools[struct{}](context.Background(), client)
	if err != nil {
		t.Fatalf("AsTools error = %v", err)
	}

	transport.handle = initializeHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		encoded, _ := json.Marshal(rpcMessage{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &rpcError{Code: -32603, Message: "internal error"},
		})
		return [][]byte{encoded}
	})
	_, err = tools[0].Exec(context.Background(), struct{}{}, json.RawMessage(`{}`))
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) {
		t.Fatalf("Exec error = %v, want *ProtocolError in the chain", err)
	}
	var retry *model.ModelRetry
	if errors.As(err, &retry) {
		t.Error("a protocol failure must not be a correctable rejection")
	}
}

func TestComposesWithAgent(t *testing.T) {
	transport := &funcTransport{handle: initializeHandler(t, toolListReply(
		`{"tools":[{"name":"echo","description":"Echo a message.","inputSchema":{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}}]}`))}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	tools, err := AsTools[struct{}](context.Background(), client)
	if err != nil {
		t.Fatalf("AsTools error = %v", err)
	}
	transport.handle = initializeHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		return [][]byte{serverReply(id, `{"content":[{"type":"text","text":"echo: hi"}],"isError":false}`)}
	})

	fakeModel := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "echo", Args: json.RawMessage(`{"msg":"hi"}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "the server said hi"}},
	)
	agent, err := golem.New[struct{}, string](fakeModel,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](tools...),
	)
	if err != nil {
		t.Fatalf("golem.New error = %v", err)
	}
	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "echo hi")
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if result.Output != "the server said hi" {
		t.Errorf("Output = %q, want %q", result.Output, "the server said hi")
	}
	var toolEvidence string
	for _, message := range result.Messages {
		if message.Role == model.RoleTool {
			toolEvidence = message.Content
		}
	}
	if toolEvidence != "echo: hi" {
		t.Errorf("tool result message = %q, want the server's text", toolEvidence)
	}
}
