// Command mcp-elicitation shows the mcp package's modern-era elicitation
// flow: a tool call that cannot finish in one round trip asks the client
// for input, the client answers through OnElicitation, and the call is
// retried with the answer and the server's opaque state. The example
// binary doubles as the server — running it with the "serve" argument
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

	// Elicitation is answered by the application, never by the model.
	// This handler is deterministic so the example stays offline; a real
	// one would prompt the user and respect a decline.
	client.OnElicitation(func(_ context.Context, request mcp.Elicitation) (mcp.ElicitationResult, error) {
		fmt.Printf("elicitation (%s): %s\n", request.Mode, request.Message)
		if request.Mode == mcp.ElicitationModeForm {
			fmt.Printf("  requested schema: %s\n", request.RequestedSchema)
		}
		if request.Mode == mcp.ElicitationModeURL {
			// Never fetch or open the URL. Show it in full and ask
			// for explicit consent before accepting.
			fmt.Printf("  user must visit: %s\n", request.URL)
			return mcp.ElicitationResult{Action: mcp.ElicitationDecline}, nil
		}
		return mcp.ElicitationResult{
			Action:  mcp.ElicitationAccept,
			Content: json.RawMessage(`{"name":"Ada"}`),
		}, nil
	})

	ctx := context.Background()
	if err := client.Initialize(ctx); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	fmt.Printf("connected to %s %s (%s)\n\n",
		client.Server().Name, client.Server().Version, client.ProtocolVersion())

	// The server declares the tool destructive, which is exactly why it
	// asks before deploying. Golem surfaces the hint; the server still
	// has to do the asking.
	infos, err := client.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("ListTools: %w", err)
	}
	for _, info := range infos {
		fmt.Printf("tool %s: destructive=%s\n", info.Name, hint(info.Annotations.DestructiveHint))
	}
	fmt.Println()

	tools, err := mcp.AsTools[struct{}](ctx, client)
	if err != nil {
		return fmt.Errorf("AsTools: %w", err)
	}

	fakeModel := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "deploy", Args: json.RawMessage(`{"target":"production"}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "the deployment was approved"}},
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
	result, err := agent.Run(ctx, golem.RunContext[struct{}]{}, "deploy to production")
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}
	for _, message := range result.Messages {
		if message.Role == model.RoleTool {
			fmt.Printf("deploy tool returned: %s\n", message.Content)
		}
	}
	fmt.Println("answer:", result.Output)
	return nil
}

// serve is a minimal modern MCP stdio server. Its deploy tool needs a
// confirmation, so its first tools/call answer is an input_required
// result; the client answers and retries with inputResponses and the
// echoed requestState.
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
			Params struct {
				InputResponses map[string]json.RawMessage `json:"inputResponses"`
				RequestState   json.RawMessage            `json:"requestState"`
			} `json:"params"`
		}
		if json.Unmarshal([]byte(line), &message) != nil || message.Method == "" || len(message.ID) == 0 {
			continue
		}
		switch message.Method {
		case "server/discover":
			reply(out, message.ID, `{"resultType":"complete","supportedVersions":["2026-07-28"],"capabilities":{"tools":{}},"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"deploy-server","version":"1.0"}}}`)
		case "tools/list":
			reply(out, message.ID, `{"tools":[{"name":"deploy","title":"Deploy","description":"Deploy the application to an environment.","inputSchema":{"type":"object","properties":{"target":{"type":"string"}},"required":["target"]},"outputSchema":{"type":"object","properties":{"deployed":{"type":"string"},"approvedBy":{"type":"string"}},"required":["deployed"]},"annotations":{"title":"Deploy","destructiveHint":true,"openWorldHint":false}}]}`)
		case "tools/call":
			if len(message.Params.InputResponses) == 0 {
				// The server is stateless: it encodes what it needs in
				// requestState and verifies the echo on the retry.
				reply(out, message.ID, `{"resultType":"input_required","inputRequests":{"confirm":{"method":"elicitation/create","params":{"message":"Approve deploying to production?","mode":"form","requestedSchema":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}}}},"requestState":"deploy-1"}`)
				continue
			}
			if string(message.Params.RequestState) != `"deploy-1"` {
				replyError(out, message.ID, -32602, "missing or mismatched requestState")
				continue
			}
			var answer struct {
				Action  string `json:"action"`
				Content struct {
					Name string `json:"name"`
				} `json:"content"`
			}
			if err := json.Unmarshal(message.Params.InputResponses["confirm"], &answer); err != nil || answer.Action != mcp.ElicitationAccept {
				reply(out, message.ID, `{"structuredContent":{"deployed":"none"},"isError":false}`)
				continue
			}
			structured, err := json.Marshal(map[string]any{
				"deployed":   "production",
				"approvedBy": answer.Content.Name,
			})
			if err != nil {
				replyError(out, message.ID, -32603, "encode result")
				continue
			}
			reply(out, message.ID, fmt.Sprintf(`{"structuredContent":%s,"isError":false}`, structured))
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

// hint renders a declared-or-not annotation hint. Nil means the server
// did not declare it.
func hint(value *bool) string {
	if value == nil {
		return "unset"
	}
	if *value {
		return "true"
	}
	return "false"
}
