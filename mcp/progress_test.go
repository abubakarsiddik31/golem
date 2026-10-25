package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// sentParams returns the params of the most recent sent message with
// method.
func sentParams(t *testing.T, transport *funcTransport, method string) json.RawMessage {
	t.Helper()
	for i := len(transport.sent) - 1; i >= 0; i-- {
		message := decodeSent(t, transport, i)
		if message.Method == method {
			return message.Params
		}
	}
	t.Fatalf("no %s message was sent", method)
	return nil
}

// metaOf decodes a request's _meta object.
func metaOf(t *testing.T, params json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var request struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		t.Fatalf("params %s: %v", params, err)
	}
	return request.Meta
}

// progressToken returns the request's progress token as JSON text.
func progressToken(t *testing.T, params json.RawMessage) string {
	t.Helper()
	token, ok := metaOf(t, params)["progressToken"]
	if !ok {
		t.Fatalf("params %s carry no progressToken", params)
	}
	return string(token)
}

// progressNote builds a notifications/progress message for one token.
func progressNote(t *testing.T, token string, completed, total float64, message string) []byte {
	t.Helper()
	params, err := json.Marshal(map[string]any{
		"progressToken": json.RawMessage(token),
		"progress":      completed,
		"total":         total,
		"message":       message,
	})
	if err != nil {
		t.Fatalf("marshal progress params: %v", err)
	}
	encoded, err := json.Marshal(rpcMessage{
		JSONRPC: "2.0",
		Method:  progressNotification,
		Params:  params,
	})
	if err != nil {
		t.Fatalf("marshal progress notification: %v", err)
	}
	return encoded
}

func TestOnProgressDeliversNotifications(t *testing.T) {
	t.Parallel()

	var updates []Progress
	transport := &funcTransport{}
	transport.handle = initializeHandler(t, func(method string, id json.RawMessage, params json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		token := progressToken(t, params)
		return [][]byte{
			progressNote(t, token, 1, 3, "working"),
			progressNote(t, token, 2, 3, ""),
			serverReply(id, `{"content":[{"type":"text","text":"done"}]}`),
		}
	})
	client := NewClient(transport)
	client.OnProgress(func(update Progress) { updates = append(updates, update) })
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}

	result, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if result.Text() != "done" {
		t.Fatalf("CallTool result = %q, want done", result.Text())
	}
	want := []Progress{{Completed: 1, Total: 3, Message: "working"}, {Completed: 2, Total: 3}}
	if len(updates) != len(want) {
		t.Fatalf("progress updates = %+v, want %+v", updates, want)
	}
	for i := range want {
		if updates[i] != want[i] {
			t.Errorf("update %d = %+v, want %+v", i, updates[i], want[i])
		}
	}
}

func TestProgressTokenIsAttachedOnlyWithAHandler(t *testing.T) {
	t.Parallel()

	transport := &funcTransport{handle: initializeHandler(t, toolListReply(`{"tools":[]}`))}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}

	// No handler: a legacy request carries no _meta at all.
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools error = %v", err)
	}
	if meta := metaOf(t, sentParams(t, transport, "tools/list")); len(meta) != 0 {
		t.Fatalf("_meta without a handler = %v, want none", meta)
	}

	// With a handler, the request asks for progress.
	client.OnProgress(func(Progress) {})
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools error = %v", err)
	}
	if token := progressToken(t, sentParams(t, transport, "tools/list")); token != "1" {
		t.Fatalf("progressToken = %s, want 1", token)
	}

	// Removing the handler stops asking, and the next request gets a
	// fresh token rather than reusing the old one.
	client.OnProgress(nil)
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools error = %v", err)
	}
	if meta := metaOf(t, sentParams(t, transport, "tools/list")); len(meta) != 0 {
		t.Fatalf("_meta after removing the handler = %v, want none", meta)
	}
}

func TestModernProgressTokenJoinsRequiredMetadata(t *testing.T) {
	t.Parallel()

	transport := &funcTransport{handle: modernHandler(t, func(method string, id json.RawMessage, params json.RawMessage) [][]byte {
		if method != "tools/list" {
			return nil
		}
		return [][]byte{serverReply(id, `{"resultType":"complete","tools":[]}`)}
	})}
	client := NewClient(transport)
	client.OnProgress(func(Progress) {})
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools error = %v", err)
	}

	meta := metaOf(t, sentParams(t, transport, "tools/list"))
	for _, key := range []string{"io.modelcontextprotocol/protocolVersion", "io.modelcontextprotocol/clientInfo", "progressToken"} {
		if _, ok := meta[key]; !ok {
			t.Errorf("_meta is missing %s: %v", key, meta)
		}
	}
}

func TestProgressNotificationForAnotherTokenIsDropped(t *testing.T) {
	t.Parallel()

	var updates []Progress
	transport := &funcTransport{}
	transport.handle = initializeHandler(t, func(method string, id json.RawMessage, params json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		token := progressToken(t, params)
		// A late notification from an earlier call carries its own
		// token and must not be reported as this call's progress.
		return [][]byte{
			progressNote(t, token, 1, 2, "current"),
			progressNote(t, `"other"`, 1, 2, "stale"),
			serverReply(id, `{"content":[{"type":"text","text":"done"}]}`),
		}
	})
	client := NewClient(transport)
	client.OnProgress(func(update Progress) { updates = append(updates, update) })
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	if _, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("CallTool error = %v", err)
	}

	if len(updates) != 1 || updates[0].Message != "current" {
		t.Fatalf("progress updates = %+v, want only the matching token", updates)
	}
}

func TestMalformedProgressNotificationIsIgnored(t *testing.T) {
	t.Parallel()

	called := false
	client := NewClient(&funcTransport{})
	client.OnProgress(func(Progress) { called = true })
	client.progressToken = 1
	client.deliverProgress(json.RawMessage(`not json`))
	client.deliverProgress(json.RawMessage(`{"progressToken":`))
	if called {
		t.Fatal("handler ran for a malformed notification")
	}
}
