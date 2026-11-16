// Command mcp-server exposes a scripted Golem agent as an MCP tool and
// calls it through the HTTP handler, all offline.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/mcpserver"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
)

func main() {
	agent, err := golem.New[struct{}, string](testmodel.New().Respond(model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "Hello from the Golem agent."},
	}), golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
		return response.Message.Content, nil
	}))
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}
	server, err := mcpserver.New(agent, mcpserver.Config[struct{}, string]{
		Name:        "assistant",
		Description: "Ask the example assistant a question.",
	})
	if err != nil {
		fmt.Println("mcpserver.New:", err)
		return
	}

	httpServer := httptest.NewServer(http.HandlerFunc(server.ServeHTTP))
	defer httpServer.Close()

	for _, request := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"assistant","arguments":{"prompt":"Say hello."}}}`,
	} {
		response, err := http.Post(httpServer.URL, "application/json", strings.NewReader(request))
		if err != nil {
			fmt.Println("post:", err)
			return
		}
		var body json.RawMessage
		_ = json.NewDecoder(response.Body).Decode(&body)
		response.Body.Close()
		fmt.Printf("%s\n", body)
	}
}
