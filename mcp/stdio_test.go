package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary double as a real MCP stdio server: the
// end-to-end tests spawn os.Args[0] with serverMode set, exercising
// process startup, pipe framing, and teardown for real.
const serverMode = "GOLEM_MCP_TEST_SERVER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(serverMode); mode != "" {
		serveStdioForTests(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// serveStdioForTests is a minimal MCP server: it answers initialize,
// reports one echo tool, and calls it. Mode "hang" stops responding
// after initialize so timeout paths are exercisable.
func serveStdioForTests(mode string) {
	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	for {
		line, err := in.ReadString('\n')
		if err != nil {
			return // stdin closed: the client went away
		}
		var message rpcMessage
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &message) != nil || message.Method == "" {
			continue // a reply the server sent or malformed input
		}
		if len(message.ID) == 0 {
			continue // notification
		}
		switch message.Method {
		case "initialize":
			writeTestMessage(out, rpcMessage{JSONRPC: "2.0", ID: message.ID, Result: json.RawMessage(
				`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"golem-test-server","version":"1.0"}}`)})
			if mode == "hang" {
				time.Sleep(10 * time.Second)
			}
		case "tools/list":
			writeTestMessage(out, rpcMessage{JSONRPC: "2.0", ID: message.ID, Result: json.RawMessage(
				`{"tools":[{"name":"echo","description":"Echo a message.","inputSchema":{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}}]}`)})
		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if json.Unmarshal(message.Params, &params) != nil || params.Name != "echo" {
				writeTestMessage(out, rpcMessage{JSONRPC: "2.0", ID: message.ID, Error: &rpcError{
					Code: -32602, Message: "unknown tool",
				}})
				continue
			}
			var arguments struct {
				Msg string `json:"msg"`
			}
			_ = json.Unmarshal(params.Arguments, &arguments)
			result := json.RawMessage(jsonEncode(map[string]any{
				"content": []map[string]string{{"type": "text", "text": "echo: " + arguments.Msg}},
				"isError": false,
			}))
			writeTestMessage(out, rpcMessage{JSONRPC: "2.0", ID: message.ID, Result: result})
		default:
			writeTestMessage(out, rpcMessage{JSONRPC: "2.0", ID: message.ID, Error: &rpcError{
				Code: methodNotFound, Message: "method not found: " + message.Method,
			}})
		}
	}
}

func writeTestMessage(out *bufio.Writer, message rpcMessage) {
	encoded, err := json.Marshal(message)
	if err != nil {
		return
	}
	out.Write(encoded)
	out.WriteByte('\n')
	out.Flush()
}

func jsonEncode(v any) string {
	encoded, _ := json.Marshal(v)
	return string(encoded)
}

func TestStdioValidatesConfig(t *testing.T) {
	if _, err := NewStdio(StdioConfig{}); err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Errorf("NewStdio error = %v, want command required", err)
	}
	if _, err := NewStdio(StdioConfig{Command: os.Args[0], Env: []string{"BROKEN"}}); err == nil || !strings.Contains(err.Error(), "KEY=VALUE") {
		t.Errorf("NewStdio error = %v, want KEY=VALUE validation", err)
	}
}

func TestStdioMissingExecutable(t *testing.T) {
	_, err := NewStdio(StdioConfig{Command: "golem-definitely-not-a-binary"})
	if err == nil {
		t.Fatal("NewStdio should fail for a missing executable")
	}
}

func TestStdioEndToEnd(t *testing.T) {
	transport, err := NewStdio(StdioConfig{Command: os.Args[0], Env: []string{serverMode + "=1"}})
	if err != nil {
		t.Fatalf("NewStdio error = %v", err)
	}
	client := NewClient(transport)
	defer client.Close()

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	if got := client.Server(); got.Name != "golem-test-server" {
		t.Errorf("Server() = %+v, want golem-test-server", got)
	}

	tools, err := AsTools[struct{}](context.Background(), client)
	if err != nil {
		t.Fatalf("AsTools error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("AsTools = %+v, want one echo tool", tools)
	}

	result, err := tools[0].Exec(context.Background(), struct{}{}, json.RawMessage(`{"msg":"hi"}`))
	if err != nil {
		t.Fatalf("Exec error = %v", err)
	}
	if result != "echo: hi" {
		t.Errorf("Exec result = %q, want %q", result, "echo: hi")
	}
}

func TestStdioDeadlineHonored(t *testing.T) {
	transport, err := NewStdio(StdioConfig{Command: os.Args[0], Env: []string{serverMode + "=hang"}})
	if err != nil {
		t.Fatalf("NewStdio error = %v", err)
	}
	client := NewClient(transport)
	defer client.Close()

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err = client.CallTool(ctx, "echo", json.RawMessage(`{"msg":"hi"}`))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("CallTool error = %v, want context.DeadlineExceeded in the chain", err)
	}
}
