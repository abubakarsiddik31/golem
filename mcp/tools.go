package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// AsTools discovers the client server's tools and returns each as an
// ordinary tool.Tool[Deps] carrying the server's name, description,
// and input schema verbatim: the server's schema is the model-facing
// contract. Executing a tool calls tools/call with the model's raw
// arguments.
//
// A result flagged isError rejects as a correctable *model.ModelRetry
// — the model sees the server's explanation and may retry with fixed
// arguments under the agent's tool retry budget. Protocol and
// transport failures surface as errors (typed where the protocol
// types them) and fail the run at the tool stage.
//
// The snapshot is point-in-time: re-run AsTools to pick up a server's
// changed tool list.
func AsTools[Deps any](ctx context.Context, client *Client) ([]tool.Tool[Deps], error) {
	infos, err := client.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	tools := make([]tool.Tool[Deps], 0, len(infos))
	for _, info := range infos {
		bridged, err := asTool[Deps](client, info)
		if err != nil {
			return nil, err
		}
		tools = append(tools, bridged)
	}
	return tools, nil
}

// asTool bridges one server tool.
func asTool[Deps any](client *Client, info ToolInfo) (tool.Tool[Deps], error) {
	schema := info.InputSchema
	if len(schema) == 0 {
		// Some servers omit the schema; an empty-object schema keeps
		// the tool registrable.
		schema = json.RawMessage(`{"type":"object"}`)
	}
	return tool.New(tool.Tool[Deps]{
		Name:        info.Name,
		Description: info.Description,
		Schema:      schema,
		Exec: func(ctx context.Context, deps Deps, args json.RawMessage) (string, error) {
			result, err := client.CallTool(ctx, info.Name, args)
			if err != nil {
				return "", err
			}
			if result.IsError {
				return "", &model.ModelRetry{Err: serverToolError(info.Name, result)}
			}
			return result.Text(), nil
		},
	})
}

// serverToolError describes an isError tool result for the model.
func serverToolError(name string, result CallResult) error {
	text := result.Text()
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("tool %s failed", name)
	}
	return fmt.Errorf("tool %s failed: %s", name, text)
}
