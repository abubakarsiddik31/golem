// Command mcp-http shows the mcp package over the streamable-HTTP
// transport: a local HTTP server stands in for a remote MCP endpoint —
// no external network, no credentials, fully deterministic.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/mcp"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
)

func main() {
	// Stand-in for a remote MCP endpoint on the loopback interface.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Println("Listen:", err)
		return
	}
	server := &http.Server{Handler: http.HandlerFunc(handle)}
	go server.Serve(listener)
	defer server.Close()
	endpoint := "http://" + listener.Addr().String() + "/mcp"

	if err := run(endpoint); err != nil {
		fmt.Println(err)
	}
}

func run(endpoint string) error {
	// Header carries auth on every request in real deployments.
	transport, err := mcp.NewHTTP(mcp.HTTPConfig{URL: endpoint})
	if err != nil {
		return fmt.Errorf("NewHTTP: %w", err)
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
	fmt.Printf("connected to %s %s over HTTP; bridged %d tool(s)\n\n",
		client.Server().Name, client.Server().Version, len(tools))

	fakeModel := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "echo", Args: json.RawMessage(`{"msg":"hello streamable"}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "the remote server answered"}},
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
	result, err := agent.Run(ctx, golem.RunContext[struct{}]{}, "echo hello streamable")
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

// handle is a minimal streamable-HTTP MCP endpoint: initialize answers
// JSON, tools/call answers a text/event-stream — both shapes the
// transport reads.
func handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var message struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&message); err != nil || message.Method == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	switch message.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "example-session")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"remote-echo","version":"1.0"}}}`, message.ID)
	case "tools/list":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"echo","description":"Echo a message back.","inputSchema":{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}}]}}`, message.ID)
	case "tools/call":
		w.Header().Set("Content-Type", "text/event-stream")
		out := bufio.NewWriter(w)
		fmt.Fprintf(out, "data: %s\n\n", fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"echo: hello streamable"}],"isError":false}}`, message.ID))
		out.Flush()
	default:
		// Notifications and replies to server requests: accepted.
		w.WriteHeader(http.StatusAccepted)
	}
}
