// Command file-read shows the fileread common tool in action: a local
// temp directory stands in for a workspace and a scripted fake model
// requests the read — no network, no credentials, fully deterministic.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/fileread"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
)

func main() {
	// Stand-in for a workspace the agent may read.
	root, err := os.MkdirTemp("", "golem-file-read")
	if err != nil {
		fmt.Println("MkdirTemp:", err)
		return
	}
	defer os.RemoveAll(root)
	notes := filepath.Join(root, "notes")
	if err := os.Mkdir(notes, 0o755); err != nil {
		fmt.Println("Mkdir:", err)
		return
	}
	if err := os.WriteFile(filepath.Join(notes, "release.md"),
		[]byte("# Release notes\n\n- web fetch\n- run events\n"), 0o644); err != nil {
		fmt.Println("WriteFile:", err)
		return
	}

	// Reads are confined to Root: absolute paths, traversal, and
	// symlinks out of it are rejected as correctable mistakes.
	read := fileread.MustNew[struct{}](fileread.Config{Root: root})

	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{
				ID:   "call-1",
				Name: fileread.ToolName,
				Args: json.RawMessage(`{"path": "notes/release.md"}`),
			},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "two features shipped"}},
	)
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](read),
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
			fmt.Printf("read_file returned:\n%s\n\n", message.Content)
		}
	}
	fmt.Println("answer:", result.Output)
}
