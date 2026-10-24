package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestListPromptsPaginates(t *testing.T) {
	var calls int
	transport := &funcTransport{handle: initializeHandler(t, func(method string, id json.RawMessage, params json.RawMessage) [][]byte {
		if method != "prompts/list" {
			t.Errorf("unexpected method %q", method)
			return nil
		}
		calls++
		var request struct {
			Cursor string `json:"cursor"`
		}
		if err := json.Unmarshal(params, &request); err != nil {
			t.Fatalf("prompts/list params: %v", err)
		}
		switch {
		case calls == 1 && request.Cursor == "":
			return [][]byte{serverReply(id, `{"prompts":[{"name":"summarize","description":"Summarize text","arguments":[{"name":"text","required":true}]}],"nextCursor":"p1"}`)}
		case calls == 2 && request.Cursor == "p1":
			return [][]byte{serverReply(id, `{"prompts":[{"name":"translate","title":"Translate"}]}`)}
		}
		t.Errorf("prompts/list call %d with cursor %q", calls, request.Cursor)
		return nil
	})}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	prompts, err := client.ListPrompts(context.Background())
	if err != nil {
		t.Fatalf("ListPrompts error = %v", err)
	}
	if len(prompts) != 2 {
		t.Fatalf("ListPrompts returned %d prompts, want 2 across two pages", len(prompts))
	}
	first := prompts[0]
	if first.Name != "summarize" || first.Description != "Summarize text" || len(first.Arguments) != 1 {
		t.Errorf("first prompt = %+v, want the decoded entry", first)
	}
	if arg := first.Arguments[0]; arg.Name != "text" || !arg.Required {
		t.Errorf("prompt argument = %+v, want the decoded argument", arg)
	}
	if second := prompts[1]; second.Name != "translate" || second.Title != "Translate" {
		t.Errorf("second prompt = %+v, want the decoded entry", second)
	}
}

func TestGetPromptSendsArgumentsAndDecodesMessages(t *testing.T) {
	transport := &funcTransport{handle: initializeHandler(t, func(method string, id json.RawMessage, params json.RawMessage) [][]byte {
		if method != "prompts/get" {
			t.Errorf("unexpected method %q", method)
			return nil
		}
		var request struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		if err := json.Unmarshal(params, &request); err != nil {
			t.Fatalf("prompts/get params: %v", err)
		}
		if request.Name != "summarize" {
			t.Errorf("prompts/get name = %q, want summarize", request.Name)
		}
		if got := request.Arguments["text"]; got != "hi there" {
			t.Errorf("prompts/get arguments = %v, want text=hi there", request.Arguments)
		}
		return [][]byte{serverReply(id, `{"description":"A summary prompt","messages":[
			{"role":"user","content":{"type":"text","text":"Summarize: hi there"}},
			{"role":"assistant","content":{"type":"resource","resource":{"uri":"file:///a","mimeType":"text/plain","text":"embedded"}}},
			{"role":"user","content":{"type":"image","mimeType":"image/png","data":"aGVsbG8="}},
			{"role":"user","content":{"type":"resource_link","uri":"file:///b"}}
		]}`)}
	})}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	result, err := client.GetPrompt(context.Background(), "summarize", map[string]string{"text": "hi there"})
	if err != nil {
		t.Fatalf("GetPrompt error = %v", err)
	}
	if result.Description != "A summary prompt" {
		t.Errorf("Description = %q, want the server's description", result.Description)
	}
	if len(result.Messages) != 4 {
		t.Fatalf("GetPrompt returned %d messages, want 4", len(result.Messages))
	}
	if got := result.Messages[0].Text(); got != "Summarize: hi there" {
		t.Errorf("text message = %q, want the block's text", got)
	}
	if got := result.Messages[1].Text(); got != "embedded" {
		t.Errorf("embedded resource message = %q, want the resource's text", got)
	}
	if got := result.Messages[2].Text(); got != "[mcp: unsupported image content]" {
		t.Errorf("image message = %q, want an image placeholder", got)
	}
	if got := string(result.Messages[2].Content.Data); got != "hello" {
		t.Errorf("image data = %q, want the decoded bytes", got)
	}
	if got := result.Messages[3].Text(); got != "[mcp: unsupported resource_link content]" {
		t.Errorf("resource_link message = %q, want a placeholder", got)
	}
	if result.Messages[1].Role != "assistant" || result.Messages[0].Role != "user" {
		t.Errorf("roles = %q, %q, want user then assistant", result.Messages[0].Role, result.Messages[1].Role)
	}
}

func TestGetPromptOmitsEmptyArguments(t *testing.T) {
	transport := &funcTransport{handle: initializeHandler(t, func(method string, id json.RawMessage, params json.RawMessage) [][]byte {
		if method != "prompts/get" {
			return nil
		}
		var request map[string]json.RawMessage
		if err := json.Unmarshal(params, &request); err != nil {
			t.Fatalf("prompts/get params: %v", err)
		}
		if _, ok := request["arguments"]; ok {
			t.Errorf("prompts/get params = %s, want no arguments member", params)
		}
		return [][]byte{serverReply(id, `{"messages":[{"role":"user","content":{"type":"text","text":"hi"}}]}`)}
	})}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	result, err := client.GetPrompt(context.Background(), "greet", nil)
	if err != nil {
		t.Fatalf("GetPrompt error = %v", err)
	}
	if len(result.Messages) != 1 || result.Messages[0].Text() != "hi" {
		t.Errorf("GetPrompt = %+v, want the rendered message", result)
	}
}

func TestGetPromptRejectsEmptyName(t *testing.T) {
	transport := &funcTransport{}
	client := NewClient(transport)
	if _, err := client.GetPrompt(context.Background(), "", nil); err == nil || !strings.Contains(err.Error(), "prompt name is empty") {
		t.Errorf("GetPrompt error = %v, want an empty-name failure", err)
	}
	if len(transport.sent) != 0 {
		t.Errorf("client sent %d messages, want none for an invalid name", len(transport.sent))
	}
}

func TestPromptMessageText(t *testing.T) {
	cases := []struct {
		name    string
		message PromptMessage
		want    string
	}{
		{"text", PromptMessage{Content: PromptContent{Type: "text", Text: "hello"}}, "hello"},
		{"text resource", PromptMessage{Content: PromptContent{Type: "resource", Resource: &ResourceContent{Text: "body"}}}, "body"},
		{"blob resource", PromptMessage{Content: PromptContent{Type: "resource", Resource: &ResourceContent{Blob: []byte("x")}}}, "[mcp: unsupported resource content]"},
		{"resource without payload", PromptMessage{Content: PromptContent{Type: "resource"}}, "[mcp: unsupported resource content]"},
		{"audio", PromptMessage{Content: PromptContent{Type: "audio"}}, "[mcp: unsupported audio content]"},
		{"unknown", PromptMessage{Content: PromptContent{Type: "weird"}}, "[mcp: unsupported weird content]"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.message.Text(); got != testCase.want {
				t.Errorf("Text() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestPromptOperationsRequireInitialization(t *testing.T) {
	client := NewClient(&funcTransport{})
	ctx := context.Background()
	if _, err := client.ListPrompts(ctx); err != errNotInitialized {
		t.Errorf("ListPrompts error = %v, want %v", err, errNotInitialized)
	}
	if _, err := client.GetPrompt(ctx, "greet", nil); err != errNotInitialized {
		t.Errorf("GetPrompt error = %v, want %v", err, errNotInitialized)
	}
}

func TestModernPromptOperationsCarryMetadata(t *testing.T) {
	transport := &funcTransport{handle: modernHandler(t, func(method string, id json.RawMessage, params json.RawMessage) [][]byte {
		assertModernMeta(t, method, params)
		switch method {
		case "prompts/list":
			return [][]byte{serverReply(id, `{"resultType":"complete","prompts":[{"name":"greet"}]}`)}
		case "prompts/get":
			return [][]byte{serverReply(id, `{"resultType":"complete","messages":[{"role":"user","content":{"type":"text","text":"hi"}}]}`)}
		}
		t.Errorf("unexpected method %q", method)
		return nil
	})}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	ctx := context.Background()
	if _, err := client.ListPrompts(ctx); err != nil {
		t.Fatalf("ListPrompts error = %v", err)
	}
	if _, err := client.GetPrompt(ctx, "greet", map[string]string{"name": "Ada"}); err != nil {
		t.Fatalf("GetPrompt error = %v", err)
	}
}
