// Command deferred-tools runs the full deferred-tool cycle offline
// against a scripted model: no network, no credentials, deterministic.
// A gated file-deletion tool pauses the run for approval; the
// application denies one call and approves another; the resumed
// conversation answers with the outcomes the model can see.
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

func main() {
	if err := run(); err != nil {
		fmt.Println(err)
	}
}

func run() error {
	// A workspace-mutating tool that gates its action on approval: the
	// deferred pass stops before any side effect, and the approved
	// re-run performs the work.
	type workspace struct{ Deletions []string }
	deleteFile := tool.MustNew(tool.Tool[*workspace]{
		Name:        "delete_file",
		Description: "Delete a workspace file.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		Exec: func(ctx context.Context, ws *workspace, args json.RawMessage) (string, error) {
			if !tool.CallApproved(ctx) {
				return "", &tool.Deferred{Kind: tool.DeferApproval, Reason: "deleting workspace files needs sign-off"}
			}
			var request struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &request); err != nil {
				return "", fmt.Errorf("decode path: %w", err)
			}
			ws.Deletions = append(ws.Deletions, request.Path)
			return fmt.Sprintf("deleted %s", request.Path), nil
		},
	})

	client := testmodel.New().Respond(
		// Turn one: the model asks to delete two files.
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "delete_file", Args: json.RawMessage(`{"path":"scratch.txt"}`)},
			{ID: "call-2", Name: "delete_file", Args: json.RawMessage(`{"path":"final-report.txt"}`)},
		}}},
		// Turn two: after resume, the model reports the outcomes.
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Deleted scratch.txt; final-report.txt was denied."}},
	)
	agent, err := golem.New[*workspace, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[*workspace, string](deleteFile),
	)
	if err != nil {
		return fmt.Errorf("golem.New: %w", err)
	}

	// The run pauses: Pending lists what needs a human decision, and the
	// tool has not deleted anything yet.
	deps := &workspace{}
	result, err := agent.Run(context.Background(), golem.RunContext[*workspace]{Deps: deps}, "clean up the workspace")
	if err != nil {
		return fmt.Errorf("Run: %w", err)
	}
	if result.Pending == nil {
		return fmt.Errorf("expected the run to pause on deferred calls")
	}
	fmt.Println("paused; approvals requested:")
	for _, pending := range result.Pending.Approvals {
		fmt.Printf("  %s (%s): %s\n", pending.ToolName, pending.Reason, pending.Args)
	}
	fmt.Printf("deletions so far: %v\n", deps.Deletions)

	// The human denies the report and approves the scratch file. The
	// approved call re-executes the tool with the marker set.
	resumed, err := agent.RunWithDeferredResults(context.Background(),
		golem.RunContext[*workspace]{Deps: deps}, result.Messages,
		golem.DeferredResults{Approvals: map[string]golem.Approval{
			"call-1": {Approved: true},
			"call-2": {Reason: "the final report stays"},
		}}, "")
	if err != nil {
		return fmt.Errorf("RunWithDeferredResults: %w", err)
	}

	fmt.Println("resumed:", resumed.Output)
	fmt.Printf("deletions after resume: %v\n", deps.Deletions)
	for i, request := range client.Requests() {
		fmt.Printf("request %d: messages=%d tools=%d\n", i, len(request.Messages), len(request.ToolSpecs))
	}
	return nil
}
