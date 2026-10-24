package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestListResourcesPaginates(t *testing.T) {
	var calls int
	transport := &funcTransport{handle: initializeHandler(t, func(method string, id json.RawMessage, params json.RawMessage) [][]byte {
		if method != "resources/list" {
			t.Errorf("unexpected method %q", method)
			return nil
		}
		calls++
		var request struct {
			Cursor string `json:"cursor"`
		}
		if err := json.Unmarshal(params, &request); err != nil {
			t.Fatalf("resources/list params: %v", err)
		}
		switch {
		case calls == 1 && request.Cursor == "":
			return [][]byte{serverReply(id, `{"resources":[{"uri":"file:///a","name":"a","mimeType":"text/plain","size":3}],"nextCursor":"c1"}`)}
		case calls == 2 && request.Cursor == "c1":
			return [][]byte{serverReply(id, `{"resources":[{"uri":"file:///b","name":"b","title":"B","description":"the b file"}]}`)}
		}
		t.Errorf("resources/list call %d with cursor %q", calls, request.Cursor)
		return nil
	})}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	resources, err := client.ListResources(context.Background())
	if err != nil {
		t.Fatalf("ListResources error = %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("ListResources returned %d resources, want 2 across two pages", len(resources))
	}
	first := resources[0]
	if first.URI != "file:///a" || first.Name != "a" || first.MIMEType != "text/plain" || first.Size != 3 {
		t.Errorf("first resource = %+v, want the decoded list entry", first)
	}
	second := resources[1]
	if second.URI != "file:///b" || second.Title != "B" || second.Description != "the b file" {
		t.Errorf("second resource = %+v, want the decoded list entry", second)
	}
}

func TestListResourceTemplatesPaginates(t *testing.T) {
	transport := &funcTransport{handle: initializeHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "resources/templates/list" {
			t.Errorf("unexpected method %q", method)
			return nil
		}
		return [][]byte{serverReply(id, `{"resourceTemplates":[{"uriTemplate":"file:///{path}","name":"files","title":"Files","mimeType":"text/plain"}]}`)}
	})}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	templates, err := client.ListResourceTemplates(context.Background())
	if err != nil {
		t.Fatalf("ListResourceTemplates error = %v", err)
	}
	if len(templates) != 1 {
		t.Fatalf("ListResourceTemplates returned %d templates, want 1", len(templates))
	}
	template := templates[0]
	if template.URITemplate != "file:///{path}" || template.Name != "files" || template.Title != "Files" || template.MIMEType != "text/plain" {
		t.Errorf("template = %+v, want the decoded template", template)
	}
}

func TestReadResourceDecodesTextAndBlob(t *testing.T) {
	transport := &funcTransport{handle: initializeHandler(t, func(method string, id json.RawMessage, params json.RawMessage) [][]byte {
		if method != "resources/read" {
			t.Errorf("unexpected method %q", method)
			return nil
		}
		var request struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(params, &request); err != nil {
			t.Fatalf("resources/read params: %v", err)
		}
		if request.URI != "file:///notes" {
			t.Errorf("resources/read uri = %q, want file:///notes", request.URI)
		}
		return [][]byte{serverReply(id, `{"contents":[
			{"uri":"file:///notes","mimeType":"text/plain","text":"hello"},
			{"uri":"file:///logo.png","mimeType":"image/png","blob":"aGVsbG8="}
		]}`)}
	})}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	contents, err := client.ReadResource(context.Background(), "file:///notes")
	if err != nil {
		t.Fatalf("ReadResource error = %v", err)
	}
	if len(contents) != 2 {
		t.Fatalf("ReadResource returned %d contents, want 2", len(contents))
	}
	if contents[0].Text != "hello" || contents[0].MIMEType != "text/plain" || contents[0].Blob != nil {
		t.Errorf("text content = %+v, want the text payload only", contents[0])
	}
	if got := string(contents[1].Blob); got != "hello" {
		t.Errorf("blob content = %q, want the decoded bytes", got)
	}
	if contents[1].MIMEType != "image/png" || contents[1].Text != "" {
		t.Errorf("blob content = %+v, want the decoded payload only", contents[1])
	}
}

func TestReadResourceRejectsEmptyURI(t *testing.T) {
	transport := &funcTransport{}
	client := NewClient(transport)
	if _, err := client.ReadResource(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "URI is empty") {
		t.Errorf("ReadResource error = %v, want an empty-URI failure", err)
	}
	if len(transport.sent) != 0 {
		t.Errorf("client sent %d messages, want none for an invalid URI", len(transport.sent))
	}
}

func TestResourceOperationsRequireInitialization(t *testing.T) {
	client := NewClient(&funcTransport{})
	ctx := context.Background()
	if _, err := client.ListResources(ctx); err != errNotInitialized {
		t.Errorf("ListResources error = %v, want %v", err, errNotInitialized)
	}
	if _, err := client.ListResourceTemplates(ctx); err != errNotInitialized {
		t.Errorf("ListResourceTemplates error = %v, want %v", err, errNotInitialized)
	}
	if _, err := client.ReadResource(ctx, "file:///a"); err != errNotInitialized {
		t.Errorf("ReadResource error = %v, want %v", err, errNotInitialized)
	}
}

func TestModernResourceOperationsCarryMetadata(t *testing.T) {
	transport := &funcTransport{handle: modernHandler(t, func(method string, id json.RawMessage, params json.RawMessage) [][]byte {
		assertModernMeta(t, method, params)
		switch method {
		case "resources/list":
			return [][]byte{serverReply(id, `{"resultType":"complete","resources":[{"uri":"file:///a","name":"a"}]}`)}
		case "resources/templates/list":
			return [][]byte{serverReply(id, `{"resultType":"complete","resourceTemplates":[{"uriTemplate":"file:///{p}","name":"t"}]}`)}
		case "resources/read":
			return [][]byte{serverReply(id, `{"resultType":"complete","contents":[{"uri":"file:///a","text":"x"}]}`)}
		}
		t.Errorf("unexpected method %q", method)
		return nil
	})}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	ctx := context.Background()
	if _, err := client.ListResources(ctx); err != nil {
		t.Fatalf("ListResources error = %v", err)
	}
	if _, err := client.ListResourceTemplates(ctx); err != nil {
		t.Fatalf("ListResourceTemplates error = %v", err)
	}
	if _, err := client.ReadResource(ctx, "file:///a"); err != nil {
		t.Fatalf("ReadResource error = %v", err)
	}
}
