package mcpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/mcpserver"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
)

func newServer(t *testing.T, modelClient model.StreamingModel, cfg mcpserver.Config[struct{}, string]) *mcpserver.Server[struct{}, string] {
	t.Helper()
	agent, err := golem.New[struct{}, string](modelClient,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}))
	if err != nil {
		t.Fatalf("golem.New() error = %v", err)
	}
	if cfg.Name == "" {
		cfg.Name = "assistant"
	}
	if cfg.Description == "" {
		cfg.Description = "Ask the assistant."
	}
	server, err := mcpserver.New(agent, cfg)
	if err != nil {
		t.Fatalf("mcpserver.New() error = %v", err)
	}
	return server
}

func answer(text string) model.Response {
	return model.Response{Message: model.Message{Role: model.RoleAssistant, Content: text}}
}

func handle(t *testing.T, server *mcpserver.Server[struct{}, string], request string) map[string]any {
	t.Helper()
	response, ok := server.Handle(context.Background(), []byte(request))
	if !ok {
		t.Fatalf("notification returned a response")
	}
	var decoded map[string]any
	if err := json.Unmarshal(response, &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return decoded
}

func TestProtocolInitializeListAndCall(t *testing.T) {
	t.Parallel()

	server := newServer(t, testmodel.New().Respond(answer("hello")), mcpserver.Config[struct{}, string]{})

	init := handle(t, server, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28"}}`)
	result := init["result"].(map[string]any)
	if result["protocolVersion"] != mcpserver.ModernProtocolVersion {
		t.Fatalf("protocolVersion = %v", result["protocolVersion"])
	}
	info := result["serverInfo"].(map[string]any)
	if info["name"] != "assistant" {
		t.Fatalf("serverInfo = %#v", info)
	}

	list := handle(t, server, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools := list["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "assistant" {
		t.Fatalf("tools = %#v", tools)
	}

	call := handle(t, server, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"assistant","arguments":{"prompt":"hi"}}}`)
	callResult := call["result"].(map[string]any)
	content := callResult["content"].([]any)[0].(map[string]any)
	if callResult["isError"] != false || content["text"] != "hello" {
		t.Fatalf("tools/call result = %#v", callResult)
	}

	handle(t, server, `{"jsonrpc":"2.0","id":4,"method":"ping"}`)
	unknown := handle(t, server, `{"jsonrpc":"2.0","id":5,"method":"nope"}`)
	if unknown["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Fatalf("unknown method response = %#v", unknown)
	}
}

func TestToolCallErrorsAndNotifications(t *testing.T) {
	t.Parallel()

	server := newServer(t, testmodel.New().Fail(errors.New("model offline")), mcpserver.Config[struct{}, string]{})
	call := handle(t, server, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"assistant","arguments":{"prompt":"hi"}}}`)
	result := call["result"].(map[string]any)
	if result["isError"] != true || !strings.Contains(result["content"].([]any)[0].(map[string]any)["text"].(string), "model offline") {
		t.Fatalf("failed call result = %#v", result)
	}

	if _, ok := server.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); ok {
		t.Fatal("notification returned a response")
	}
	badTool := handle(t, server, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"other","arguments":{}}}`)
	if badTool["error"].(map[string]any)["code"].(float64) != -32602 {
		t.Fatalf("unknown tool response = %#v", badTool)
	}
	badPrompt := handle(t, server, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"assistant","arguments":{}}}`)
	if badPrompt["error"].(map[string]any)["code"].(float64) != -32602 {
		t.Fatalf("missing prompt response = %#v", badPrompt)
	}
	parse := handle(t, server, `{`)
	if parse["error"].(map[string]any)["code"].(float64) != -32700 {
		t.Fatalf("parse error response = %#v", parse)
	}
}

func TestHTTPHandler(t *testing.T) {
	t.Parallel()

	server := newServer(t, testmodel.New().Respond(answer("hello")), mcpserver.Config[struct{}, string]{})
	handler := http.HandlerFunc(server.ServeHTTP)

	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"assistant","arguments":{"prompt":"hi"}}}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"text":"hello"`) {
		t.Fatalf("POST response = %d %s", recorder.Code, recorder.Body.String())
	}

	notification := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, notification)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("notification status = %d", recorder.Code)
	}

	get := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, get)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d", recorder.Code)
	}
}

func TestStdioServe(t *testing.T) {
	t.Parallel()

	server := newServer(t, testmodel.New().Respond(answer("hello")), mcpserver.Config[struct{}, string]{})
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"assistant","arguments":{"prompt":"hi"}}}`,
		"",
	}, "\n")
	var output bytes.Buffer
	transport := mcpserver.NewStdio(strings.NewReader(input), &output)
	if err := server.Serve(context.Background(), transport); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("responses = %d: %q", len(lines), output.String())
	}
	if !strings.Contains(lines[0], `"protocolVersion":"2025-06-18"`) || !strings.Contains(lines[1], `"text":"hello"`) {
		t.Fatalf("responses = %q", lines)
	}
}

func TestDepsResolutionAndFormat(t *testing.T) {
	t.Parallel()

	called := false
	server := newServer(t, testmodel.New().Respond(answer("hello")), mcpserver.Config[struct{}, string]{
		Deps: func(context.Context) (struct{}, error) {
			called = true
			return struct{}{}, nil
		},
		Format: func(output string) (string, error) {
			return "formatted: " + output, nil
		},
	})
	call := handle(t, server, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"assistant","arguments":{"prompt":"hi"}}}`)
	text := call["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"]
	if !called || text != "formatted: hello" {
		t.Fatalf("deps=%v text=%v", called, text)
	}
}

func TestNewValidation(t *testing.T) {
	t.Parallel()

	agent, _ := golem.New[struct{}, string](testmodel.New(), golem.DecodeFunc[string](func(context.Context, model.Response) (string, error) {
		return "", nil
	}))
	if _, err := mcpserver.New(agent, mcpserver.Config[struct{}, string]{}); err == nil {
		t.Fatal("empty name accepted")
	}
	if _, err := mcpserver.New[struct{}, string](nil, mcpserver.Config[struct{}, string]{Name: "x"}); err == nil {
		t.Fatal("nil agent accepted")
	}
	if _, err := mcpserver.New(agent, mcpserver.Config[struct{}, string]{Name: "x", InputSchema: json.RawMessage(`[]`)}); err == nil {
		t.Fatal("array schema accepted")
	}
}

func TestCustomPromptDepsAndFormatErrors(t *testing.T) {
	t.Parallel()

	server := newServer(t, testmodel.New().Respond(answer("hello")), mcpserver.Config[struct{}, string]{
		Prompt: func(args json.RawMessage) (string, error) {
			var input struct {
				Task string `json:"task"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return "", err
			}
			if strings.TrimSpace(input.Task) == "" {
				return "", errors.New("task is required")
			}
			return "task: " + input.Task, nil
		},
	})
	call := handle(t, server, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"assistant","arguments":{"task":"x"}}}`)
	if text := call["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"]; text != "hello" {
		t.Fatalf("custom prompt result = %v", text)
	}
	bad := handle(t, server, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"assistant","arguments":{"task":""}}}`)
	if bad["error"].(map[string]any)["code"].(float64) != -32602 {
		t.Fatalf("empty custom prompt response = %#v", bad)
	}

	depsErr := newServer(t, testmodel.New(), mcpserver.Config[struct{}, string]{
		Deps: func(context.Context) (struct{}, error) { return struct{}{}, errors.New("no tenant") },
	})
	call = handle(t, depsErr, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"assistant","arguments":{"prompt":"hi"}}}`)
	if result := call["result"].(map[string]any); result["isError"] != true {
		t.Fatalf("deps failure result = %#v", result)
	}

	formatErr := newServer(t, testmodel.New().Respond(answer("hello")), mcpserver.Config[struct{}, string]{
		Format: func(string) (string, error) { return "", errors.New("cannot format") },
	})
	call = handle(t, formatErr, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"assistant","arguments":{"prompt":"hi"}}}`)
	if result := call["result"].(map[string]any); result["isError"] != true {
		t.Fatalf("format failure result = %#v", result)
	}
}

func TestNonStringOutputUsesJSON(t *testing.T) {
	t.Parallel()

	agent, err := golem.New[struct{}, int](testmodel.New().Respond(answer("42")),
		golem.DecodeFunc[int](func(context.Context, model.Response) (int, error) { return 42, nil }))
	if err != nil {
		t.Fatalf("golem.New() error = %v", err)
	}
	server, err := mcpserver.New(agent, mcpserver.Config[struct{}, int]{Name: "answer"})
	if err != nil {
		t.Fatalf("mcpserver.New() error = %v", err)
	}
	response, ok := server.Handle(context.Background(), []byte(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"answer","arguments":{"prompt":"hi"}}}`))
	if !ok {
		t.Fatal("no response")
	}
	if !strings.Contains(string(response), `"text":"42"`) {
		t.Fatalf("response = %s", response)
	}
}

func TestProtocolFallbacksAndEmptyLists(t *testing.T) {
	t.Parallel()

	server := newServer(t, testmodel.New(), mcpserver.Config[struct{}, string]{})
	init := handle(t, server, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	if init["result"].(map[string]any)["protocolVersion"] != mcpserver.LegacyProtocolVersion {
		t.Fatalf("unknown version response = %#v", init)
	}
	if _, ok := server.Handle(context.Background(), []byte(`{"jsonrpc":"1.0","id":2,"method":"ping"}`)); !ok {
		t.Fatal("invalid version returned no response")
	}
	for _, method := range []string{"resources/list", "prompts/list"} {
		response := handle(t, server, `{"jsonrpc":"2.0","id":3,"method":"`+method+`"}`)
		if response["result"] == nil {
			t.Fatalf("%s response = %#v", method, response)
		}
	}
}

func TestStdioTransportEdges(t *testing.T) {
	t.Parallel()

	transport := mcpserver.NewStdio(strings.NewReader("one\n\n"), io.Discard)
	message, err := transport.Read(context.Background())
	if err != nil || string(message) != "one" {
		t.Fatalf("Read() = %q, %v", message, err)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := transport.Read(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Read() error = %v", err)
	}
	if err := transport.Send(ctx, []byte(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Send() error = %v", err)
	}
	if err := mcpserver.NewStdio(strings.NewReader(""), failingWriter{}).Send(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("write failure was swallowed")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

type failingTransport struct{}

func (failingTransport) Read(context.Context) ([]byte, error) { return nil, errors.New("read failed") }
func (failingTransport) Send(context.Context, []byte) error   { return nil }
func (failingTransport) Close() error                         { return nil }

func TestServePropagatesTransportErrors(t *testing.T) {
	t.Parallel()

	server := newServer(t, testmodel.New(), mcpserver.Config[struct{}, string]{})
	if err := server.Serve(context.Background(), failingTransport{}); err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Fatalf("Serve() error = %v", err)
	}
}
