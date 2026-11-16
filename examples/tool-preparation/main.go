// Command tool-preparation filters the tools advertised to the model per
// run: a non-admin caller sees the read tool but not the destructive one.
// It runs offline against a scripted model.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

type actor struct {
	Role string
}

func main() {
	readFile := tool.MustNew(tool.Tool[actor]{
		Name:        "read_file",
		Description: "Read a file from the workspace.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		Exec: func(context.Context, actor, json.RawMessage) (tool.Result, error) {
			return tool.Text("file contents"), nil
		},
	})
	deleteFile := tool.MustNew(tool.Tool[actor]{
		Name:        "delete_file",
		Description: "Delete a file from the workspace.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		Exec: func(context.Context, actor, json.RawMessage) (tool.Result, error) {
			return tool.Text("deleted"), nil
		},
	})

	client := testmodel.New().Respond(model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "The file says: file contents."},
	})
	agent, err := golem.New[actor, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[actor, string](readFile, deleteFile),
		golem.WithPrepareTools[actor, string](func(_ context.Context, runCtx golem.RunContext[actor], specs []model.ToolSpec) ([]model.ToolSpec, error) {
			if runCtx.Deps.Role == "admin" {
				return specs, nil
			}
			return keep(specs, "read_file"), nil
		}),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[actor]{
		Deps: actor{Role: "reader"},
	}, "What does notes.txt say?")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}

	fmt.Println("answer:", result.Output)
	fmt.Println("tools advertised to the model:")
	for _, spec := range client.Requests()[0].ToolSpecs {
		fmt.Println(" ", spec.Name)
	}
}

func keep(specs []model.ToolSpec, names ...string) []model.ToolSpec {
	keep := make(map[string]struct{}, len(names))
	for _, name := range names {
		keep[name] = struct{}{}
	}
	kept := make([]model.ToolSpec, 0, len(names))
	for _, spec := range specs {
		if _, ok := keep[spec.Name]; ok {
			kept = append(kept, spec)
		}
	}
	return kept
}
