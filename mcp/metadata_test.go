package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// metadataClient returns an initialized client whose tools/list answers
// with the given tool array.
func metadataClient(t *testing.T, tools string) *Client {
	t.Helper()
	transport := &funcTransport{handle: initializeHandler(t, toolListReply(tools))}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	return client
}

func TestListToolsSurfacesAnnotationsAndOutputSchema(t *testing.T) {
	t.Parallel()

	const (
		outputSchema = `{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"]}`
		tools        = `{"tools":[{
			"name":"word_count",
			"title":"Word count",
			"description":"Count words in a message.",
			"inputSchema":{"type":"object","properties":{"msg":{"type":"string"}}},
			"outputSchema":` + outputSchema + `,
			"annotations":{
				"title":"Word count",
				"readOnlyHint":true,
				"destructiveHint":false,
				"idempotentHint":true,
				"openWorldHint":false
			}
		}]}`
	)
	client := metadataClient(t, tools)
	infos, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools error = %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("ListTools returned %d tools, want 1", len(infos))
	}
	info := infos[0]
	if info.Title != "Word count" {
		t.Errorf("Title = %q, want Word count", info.Title)
	}
	if canonicalJSON(info.OutputSchema) != canonicalJSON(json.RawMessage(outputSchema)) {
		t.Errorf("OutputSchema = %s, want the server's verbatim", info.OutputSchema)
	}
	hints := info.Annotations
	if hints.Title != "Word count" {
		t.Errorf("Annotations.Title = %q, want Word count", hints.Title)
	}
	for name, hint := range map[string]*bool{
		"readOnlyHint":    hints.ReadOnlyHint,
		"destructiveHint": hints.DestructiveHint,
		"idempotentHint":  hints.IdempotentHint,
		"openWorldHint":   hints.OpenWorldHint,
	} {
		if hint == nil {
			t.Errorf("%s = nil, want a declared value", name)
		}
	}
	if !*hints.ReadOnlyHint || *hints.DestructiveHint || !*hints.IdempotentHint || *hints.OpenWorldHint {
		t.Errorf("annotations = %+v, want read-only/idempotent and not destructive/open-world", hints)
	}
}

func TestListToolsLeavesUndeclaredAnnotationsNil(t *testing.T) {
	t.Parallel()

	client := metadataClient(t, `{"tools":[{"name":"bare","inputSchema":{"type":"object"}}]}`)
	infos, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools error = %v", err)
	}
	hints := infos[0].Annotations
	if hints.ReadOnlyHint != nil || hints.DestructiveHint != nil ||
		hints.IdempotentHint != nil || hints.OpenWorldHint != nil {
		t.Fatalf("annotations = %+v, want every hint undeclared", hints)
	}
}

func TestCallToolSurfacesStructuredContent(t *testing.T) {
	t.Parallel()

	const structured = `{"count":3}`
	transport := &funcTransport{handle: initializeHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		return [][]byte{serverReply(id, `{"content":[{"type":"text","text":"3 words"}],"structuredContent":`+structured+`}`)}
	})}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}

	result, err := client.CallTool(context.Background(), "word_count", json.RawMessage(`{"msg":"one two three"}`))
	if err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if canonicalJSON(result.StructuredContent) != canonicalJSON(json.RawMessage(structured)) {
		t.Errorf("StructuredContent = %s, want %s", result.StructuredContent, structured)
	}
	// Content wins when both are present.
	if result.Text() != "3 words" {
		t.Errorf("Text() = %q, want the content text", result.Text())
	}
}

func TestCallResultTextFallsBackToStructuredContent(t *testing.T) {
	t.Parallel()

	result := CallResult{StructuredContent: json.RawMessage(`{"count":3}`)}
	if got := result.Text(); got != `{"count":3}` {
		t.Fatalf("Text() = %q, want the structured JSON", got)
	}
	// Content still wins, and an empty result stays empty.
	if got := (CallResult{Content: []ContentItem{{Type: "text", Text: "hi"}},
		StructuredContent: json.RawMessage(`{"count":3}`)}).Text(); got != "hi" {
		t.Errorf("Text() with content = %q, want hi", got)
	}
	if got := (CallResult{}).Text(); got != "" {
		t.Errorf("Text() of an empty result = %q, want empty", got)
	}
}

func TestAsToolsReturnsStructuredContentAsText(t *testing.T) {
	t.Parallel()

	transport := &funcTransport{handle: initializeHandler(t, toolListReply(
		`{"tools":[{"name":"word_count","inputSchema":{"type":"object"},"outputSchema":{"type":"object"}}]}`))}
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
		// A structured-only result carries no content items.
		return [][]byte{serverReply(id, `{"structuredContent":{"count":3}}`)}
	})
	result, err := tools[0].Exec(context.Background(), struct{}{}, json.RawMessage(`{"msg":"one two three"}`))
	if err != nil {
		t.Fatalf("Exec error = %v", err)
	}
	if result.Text != `{"count":3}` {
		t.Fatalf("Exec text = %q, want the structured JSON", result.Text)
	}
}
