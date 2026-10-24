// Command mcp-client shows the mcp package against a Model Context
// Protocol server: tools bridged into agent tools, and resources and
// prompts read as application-driven client operations. The example
// binary doubles as that server — running it with the "serve" argument
// speaks MCP over stdio — so the whole flow is local, offline, and
// deterministic: no network, no credentials.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/mcp"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		serve()
		return
	}

	if err := run(); err != nil {
		fmt.Println(err)
	}
}

func run() error {
	// Spawn the MCP server — here, this same binary — and connect.
	transport, err := mcp.NewStdio(mcp.StdioConfig{
		Command: os.Args[0],
		Args:    []string{"serve"},
	})
	if err != nil {
		return fmt.Errorf("NewStdio: %w", err)
	}
	client := mcp.NewClient(transport)
	defer client.Close()

	ctx := context.Background()
	if err := client.Initialize(ctx); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	tools, err := mcp.AsTools[struct{}](ctx, client)
	if err != nil {
		return fmt.Errorf("AsTools: %w", err)
	}
	fmt.Printf("connected to %s %s; bridged %d tool(s)\n\n",
		client.Server().Name, client.Server().Version, len(tools))

	// The server's tools are ordinary tool.Tool values.
	fakeModel := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "echo", Args: json.RawMessage(`{"msg":"hello mcp"}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "the server echoed back"}},
	)
	agent, err := golem.New[struct{}, string](fakeModel,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](tools...),
	)
	if err != nil {
		return fmt.Errorf("golem.New: %w", err)
	}
	result, err := agent.Run(ctx, golem.RunContext[struct{}]{}, "echo hello mcp")
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}
	for _, message := range result.Messages {
		if message.Role == model.RoleTool {
			fmt.Printf("echo tool returned: %s\n", message.Content)
		}
	}
	fmt.Println("answer:", result.Output)

	// Resources and prompts are not tools: the application decides what
	// to load and what to render. Nothing here is chosen by the model.
	resources, err := client.ListResources(ctx)
	if err != nil {
		return fmt.Errorf("ListResources: %w", err)
	}
	for _, resource := range resources {
		fmt.Printf("\nresource %s (%s): %s\n", resource.URI, resource.MIMEType, resource.Description)
	}
	templates, err := client.ListResourceTemplates(ctx)
	if err != nil {
		return fmt.Errorf("ListResourceTemplates: %w", err)
	}
	for _, template := range templates {
		fmt.Printf("resource template %s: %s\n", template.URITemplate, template.Description)
	}
	if len(resources) > 0 {
		contents, err := client.ReadResource(ctx, resources[0].URI)
		if err != nil {
			return fmt.Errorf("ReadResource: %w", err)
		}
		for _, content := range contents {
			fmt.Printf("read %s (%d bytes):\n%s\n", content.URI, len(content.Text)+len(content.Blob), content.Text)
		}
	}

	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		return fmt.Errorf("ListPrompts: %w", err)
	}
	for _, prompt := range prompts {
		fmt.Printf("\nprompt %s: %s\n", prompt.Name, prompt.Description)
	}
	if len(prompts) > 0 {
		prompt, err := client.GetPrompt(ctx, prompts[0].Name, map[string]string{"name": "Ada"})
		if err != nil {
			return fmt.Errorf("GetPrompt: %w", err)
		}
		fmt.Printf("rendered %q:\n", prompt.Description)
		for _, message := range prompt.Messages {
			fmt.Printf("  %s: %s\n", message.Role, message.Text())
		}
	}
	return nil
}

// serve is a minimal MCP stdio server offering one echo tool, one
// resource, one resource template, and one prompt template.
func serve() {
	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	for {
		line, err := in.ReadString('\n')
		if err != nil {
			return
		}
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal([]byte(line), &message) != nil || message.Method == "" || len(message.ID) == 0 {
			continue
		}
		switch message.Method {
		case "server/discover":
			// A legacy server: the modern probe is refused, so the
			// client falls back to the initialize handshake.
			replyError(out, message.ID, -32601, "method not found: server/discover")
		case "initialize":
			reply(out, message.ID, `{"protocolVersion":"2025-06-18","capabilities":{"tools":{},"resources":{},"prompts":{}},"serverInfo":{"name":"echo-server","version":"1.0"}}`)
		case "tools/list":
			reply(out, message.ID, `{"tools":[{"name":"echo","description":"Echo a message back.","inputSchema":{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}}]}`)
		case "tools/call":
			reply(out, message.ID, `{"content":[{"type":"text","text":"echo: hello mcp"}],"isError":false}`)
		case "resources/list":
			reply(out, message.ID, `{"resources":[{"uri":"file:///notes/welcome.md","name":"welcome","title":"Welcome note","description":"A short greeting.","mimeType":"text/markdown","size":42}]}`)
		case "resources/templates/list":
			reply(out, message.ID, `{"resourceTemplates":[{"uriTemplate":"file:///notes/{name}","name":"notes","description":"A note by name.","mimeType":"text/markdown"}]}`)
		case "resources/read":
			reply(out, message.ID, `{"contents":[{"uri":"file:///notes/welcome.md","mimeType":"text/markdown","text":"# Welcome\n\nThe echo server is running."}]}`)
		case "prompts/list":
			reply(out, message.ID, `{"prompts":[{"name":"greet","title":"Greet","description":"Greet someone by name.","arguments":[{"name":"name","description":"Who to greet.","required":true}]}]}`)
		case "prompts/get":
			reply(out, message.ID, `{"description":"A greeting prompt","messages":[{"role":"user","content":{"type":"text","text":"Please greet Ada."}}]}`)
		}
	}
}

func reply(out *bufio.Writer, id json.RawMessage, result string) {
	encoded, err := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
	}{JSONRPC: "2.0", ID: id, Result: json.RawMessage(result)})
	if err != nil {
		return
	}
	out.Write(encoded)
	out.WriteByte('\n')
	out.Flush()
}

func replyError(out *bufio.Writer, id json.RawMessage, code int, message string) {
	encoded, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
	if err != nil {
		return
	}
	out.Write(encoded)
	out.WriteByte('\n')
	out.Flush()
}
