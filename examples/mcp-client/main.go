// Command mcp-client shows the mcp package bridging a Model Context
// Protocol server into agent tools. The example binary doubles as
// that server — running it with the "serve" argument speaks MCP over
// stdio — so the whole flow is local, offline, and deterministic:
// no network, no credentials.
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
		return fmt.Errorf("Initialize: %w", err)
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
		return fmt.Errorf("Run: %w", err)
	}
	for _, message := range result.Messages {
		if message.Role == model.RoleTool {
			fmt.Printf("echo tool returned: %s\n", message.Content)
		}
	}
	fmt.Println("answer:", result.Output)
	return nil
}

// serve is a minimal MCP stdio server offering one echo tool.
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
		case "initialize":
			reply(out, message.ID, `{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"echo-server","version":"1.0"}}`)
		case "tools/list":
			reply(out, message.ID, `{"tools":[{"name":"echo","description":"Echo a message back.","inputSchema":{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}}]}`)
		case "tools/call":
			reply(out, message.ID, `{"content":[{"type":"text","text":"echo: hello mcp"}],"isError":false}`)
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
