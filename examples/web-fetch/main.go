// Command web-fetch shows the webfetch common tool in action: a local
// test server stands in for the web and a scripted fake model requests
// the fetch — no network, no credentials, fully deterministic.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/webfetch"
)

func main() {
	// Stand-in for the web: one small HTML page with the usual noise.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/docs" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, "<html><head><title>Release notes</title>"+
			"<script>analytics()</script></head>"+
			"<body><h1>What shipped</h1><ul><li>web fetch</li><li>run events</li></ul></body></html>")
	}))
	defer server.Close()

	// An ordinary tool.Tool: composes with any agent dependency type.
	fetch := webfetch.MustNew[struct{}](webfetch.Config{Timeout: 5 * time.Second})

	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{
				ID:   "call-1",
				Name: webfetch.ToolName,
				Args: json.RawMessage(fmt.Sprintf(`{"url": %q}`, server.URL+"/docs")),
			},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "two features shipped"}},
	)
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](fetch),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "what shipped?")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	for _, message := range result.Messages {
		if message.Role == model.RoleTool {
			fmt.Printf("web_fetch returned:\n%s\n\n", message.Content)
		}
	}
	fmt.Println("answer:", result.Output)
}
