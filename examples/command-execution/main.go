// Command command-execution shows the shell common tool in action: a
// scripted fake model asks to run one local command, and a second
// scripted turn shows the command policy rejecting a denied command as
// a correctable retry — no network, no credentials, fully deterministic.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/shell"
	"github.com/abubakarsiddik31/golem/testmodel"
)

func main() {
	// An ordinary tool.Tool: registers only where running model-written
	// commands is acceptable. Dir, Env, Timeout, and MaxBytes scope it;
	// the policy fields bound what a command may be. The zero value
	// filters nothing — opt in with the exported starting-point lists.
	run := shell.MustNew[struct{}](shell.Config{
		Timeout:           10 * time.Second,
		DeniedCommands:    shell.DestructiveCommands,
		DeniedOperators:   shell.ChainingOperators,
		BlockInteractive:  true,
		DeniedEnvPatterns: shell.CredentialEnvPatterns,
	})

	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{
				ID:   "call-1",
				Name: shell.ToolName,
				Args: json.RawMessage(`{"command": "rm -rf /tmp/scratch"}`),
			},
		}}},
		// The policy denial comes back as a correctable retry, so the
		// next model turn picks a non-destructive command instead.
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{
				ID:   "call-2",
				Name: shell.ToolName,
				Args: json.RawMessage(`{"command": "echo hello from the shell tool"}`),
			},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "the command ran"}},
	)
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](run),
		// The policy denial is a correctable rejection: give the run a
		// retry budget so the model can pick another command.
		golem.WithToolRetries[struct{}, string](3),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "run the check")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	for _, message := range result.Messages {
		if message.Role == model.RoleTool {
			fmt.Printf("run_command returned:\n%s\n\n", message.Content)
		}
	}
	fmt.Println("answer:", result.Output)
}
