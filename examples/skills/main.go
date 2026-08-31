// Command skills shows the skills common tool in action: a temp
// directory laid out in the standard .agents/skills shape stands in for
// a skill pack, and a scripted fake model loads one skill — no network,
// no credentials, fully deterministic.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/skills"
	"github.com/abubakarsiddik31/golem/testmodel"
)

func main() {
	// Stand-in for the application's skill directories. The
	// application resolves the paths — a server's config directory
	// today, an OS app directory or bundled path on desktop later.
	root, err := os.MkdirTemp("", "golem-skills")
	if err != nil {
		fmt.Println("MkdirTemp:", err)
		return
	}
	defer os.RemoveAll(root)
	skillDir := filepath.Join(root, ".agents", "skills", "release-notes")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		fmt.Println("MkdirAll:", err)
		return
	}
	skill := `---
name: release-notes
description: Draft consistent release notes from merged work. Use when preparing a tagged release.
---
# Release notes

- Group merged work into Added / Fixed / Changed.
- Propose a version bump.
- End with a copy-pasteable changelog entry.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skill), 0o644); err != nil {
		fmt.Println("WriteFile:", err)
		return
	}

	// The tool description carries the catalog: only each skill's name
	// and description. Full instructions load on demand.
	loading := skills.MustNew[struct{}](skills.Config{
		Dirs: []string{filepath.Join(root, ".agents", "skills")},
	})
	fmt.Printf("tool %q catalogs %d skill(s):\n\n%s\n\n", skills.ToolName, 1, loading.Description)

	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{
				ID:   "call-1",
				Name: skills.ToolName,
				Args: json.RawMessage(`{"name": "release-notes"}`),
			},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "notes drafted"}},
	)
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](loading),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "prepare the release")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	for _, message := range result.Messages {
		if message.Role == model.RoleTool {
			fmt.Printf("skill returned:\n%s\n\n", message.Content)
		}
	}
	fmt.Println("answer:", result.Output)
}
