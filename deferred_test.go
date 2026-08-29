package golem_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// gateDeps is the dependency type of the deferred-tool fixtures.
type gateDeps struct{ Env string }

// gateRecorder records every execution of the gated deleter tool.
type gateRecorder struct {
	runs      int
	approved  bool
	deletions []string
}

// gateClient builds the standard paused-run fixture: a queued model, a
// delete_file tool that defers for approval until CallApproved reports
// true, and a string decoder.
func gateClient(t *testing.T, responses ...model.Response) (*queuedModel, *golem.Agent[gateDeps, string], *gateRecorder) {
	t.Helper()
	client := &queuedModel{responses: responses}
	rec := &gateRecorder{}
	deleteFile := tool.MustNew(tool.Tool[gateDeps]{
		Name:        "delete_file",
		Description: "Delete a file.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		Exec: func(ctx context.Context, deps gateDeps, args json.RawMessage) (string, error) {
			rec.runs++
			rec.approved = tool.CallApproved(ctx)
			if !tool.CallApproved(ctx) {
				return "", &tool.Deferred{Kind: tool.DeferApproval, Reason: "deletes need sign-off"}
			}
			rec.deletions = append(rec.deletions, string(args))
			return "deleted", nil
		},
	})
	agent, err := golem.New[gateDeps, string](
		client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}),
		golem.WithTools[gateDeps, string](deleteFile),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client, agent, rec
}

// pauseResponse is an assistant turn requesting one tool call.
func pauseResponse(toolName string) model.Response {
	return model.Response{
		Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: toolName, Args: json.RawMessage(`{"path":"notes.txt"}`)},
		}},
		Usage: model.Usage{InputTokens: 7, OutputTokens: 3},
	}
}

// pausedRun drives the first leg of the pause/resume pair and returns
// the paused result.
func pausedRun(t *testing.T, agent *golem.Agent[gateDeps, string]) golem.Result[string] {
	t.Helper()
	result, err := agent.Run(context.Background(), golem.RunContext[gateDeps]{}, "delete notes.txt")
	if err != nil {
		t.Fatalf("Run() error = %v, want a clean pause", err)
	}
	return result
}

func TestRunPausesOnApprovalDeferredTool(t *testing.T) {
	t.Parallel()

	client, agent, rec := gateClient(t, pauseResponse("delete_file"))

	result := pausedRun(t, agent)
	if rec.runs != 1 || rec.approved {
		t.Fatalf("tool ran %d times (approved=%v), want one unapproved pass", rec.runs, rec.approved)
	}
	if result.Pending == nil {
		t.Fatalf("Result.Pending is nil, want the deferred call")
	}
	if len(result.Pending.Approvals) != 1 || len(result.Pending.External) != 0 {
		t.Fatalf("pending = %#v, want one approval and no externals", result.Pending)
	}
	pending := result.Pending.Approvals[0]
	if pending.CallID != "call-1" || pending.ToolName != "delete_file" ||
		string(pending.Args) != `{"path":"notes.txt"}` || pending.Reason != "deletes need sign-off" {
		t.Fatalf("pending call = %#v", pending)
	}
	if result.Output != "" {
		t.Fatalf("paused run decoded output %q, want the zero value", result.Output)
	}
	if result.Usage != (model.Usage{InputTokens: 7, OutputTokens: 3}) {
		t.Fatalf("usage = %#v, want the pausing turn's usage", result.Usage)
	}
	// The run must end at the pause: one model call, no second turn, and
	// evidence ending with the unanswered call.
	if len(client.requests) != 1 {
		t.Fatalf("model called %d times, want 1", len(client.requests))
	}
	messages := result.Messages
	if len(messages) != 2 || messages[1].Role != model.RoleAssistant || len(messages[1].ToolCalls) != 1 {
		t.Fatalf("evidence = %#v, want user + the requesting assistant turn", messages)
	}
}

func TestRunPausesWithMixedBatchExecutesTheRest(t *testing.T) {
	t.Parallel()

	rec := &gateRecorder{}
	deleteFile := tool.MustNew(tool.Tool[gateDeps]{
		Name:        "delete_file",
		Description: "Delete a file.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps gateDeps, args json.RawMessage) (string, error) {
			rec.runs++
			return "", &tool.Deferred{Kind: tool.DeferApproval, Reason: "sign-off"}
		},
	})
	stat := tool.MustNew(tool.Tool[gateDeps]{
		Name:        "stat_file",
		Description: "Stat a file.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps gateDeps, args json.RawMessage) (string, error) {
			return "size=12", nil
		},
	})
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "delete_file", Args: json.RawMessage(`{"path":"a"}`)},
			{ID: "call-2", Name: "stat_file", Args: json.RawMessage(`{}`)},
		}}},
	}}
	agent, err := golem.New[gateDeps, string](
		client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) { return r.Message.Content, nil }),
		golem.WithTools[gateDeps, string](deleteFile, stat),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result := pausedRun(t, agent)
	if result.Pending == nil || len(result.Pending.Approvals) != 1 || result.Pending.Approvals[0].CallID != "call-1" {
		t.Fatalf("pending = %#v, want call-1 pending", result.Pending)
	}
	// The executed call's result joins the evidence; the deferred call's
	// does not.
	messages := result.Messages
	if len(messages) != 3 {
		t.Fatalf("evidence = %#v, want user, assistant, and one executed result", messages)
	}
	last := messages[2]
	if last.Role != model.RoleTool || last.ToolCallID != "call-2" || last.Content != "size=12" {
		t.Fatalf("executed result = %#v, want call-2's stat result", last)
	}
}

func TestRunPausesOnExternalDeferredTool(t *testing.T) {
	t.Parallel()

	report := tool.MustNew(tool.Tool[gateDeps]{
		Name:        "delete_file",
		Description: "Start a report build.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps gateDeps, args json.RawMessage) (string, error) {
			return "", &tool.Deferred{Kind: tool.DeferExternal, Reason: "job-7"}
		},
	})
	agent, err := golem.New[gateDeps, string](
		&queuedModel{responses: []model.Response{pauseResponse("delete_file")}},
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) { return r.Message.Content, nil }),
		golem.WithTools[gateDeps, string](report),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[gateDeps]{}, "build the report")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Pending == nil || len(result.Pending.External) != 1 || len(result.Pending.Approvals) != 0 {
		t.Fatalf("pending = %#v, want one external call", result.Pending)
	}
	if result.Pending.External[0].Reason != "job-7" {
		t.Fatalf("reason = %q, want the correlation key", result.Pending.External[0].Reason)
	}
}

func TestRunDeferredEmitsEventPerPendingCall(t *testing.T) {
	t.Parallel()

	_, agent, _ := gateClient(t, pauseResponse("delete_file"))
	var events []golem.RunEvent
	agent, err := golem.New[gateDeps, string](
		&queuedModel{responses: []model.Response{pauseResponse("delete_file")}},
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) { return r.Message.Content, nil }),
		golem.WithTools[gateDeps, string](mustGatedDeleter(t, &gateRecorder{})),
		golem.WithRunEvents[gateDeps, string](func(e golem.RunEvent) { events = append(events, e) }),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[gateDeps]{}, "delete notes.txt"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var deferred []golem.RunEvent
	for _, e := range events {
		if e.Kind == golem.EventDeferred {
			deferred = append(deferred, e)
		}
	}
	if len(deferred) != 1 {
		t.Fatalf("deferred events = %d, want 1 (all events: %#v)", len(deferred), events)
	}
	if deferred[0].CallID != "call-1" || deferred[0].ToolName != "delete_file" {
		t.Fatalf("deferred event = %#v, want the pending call identified", deferred[0])
	}
}

// mustGatedDeleter returns a standalone gated deleter for tests that
// build their own agent.
func mustGatedDeleter(t *testing.T, rec *gateRecorder) tool.Tool[gateDeps] {
	t.Helper()
	return tool.MustNew(tool.Tool[gateDeps]{
		Name:        "delete_file",
		Description: "Delete a file.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps gateDeps, args json.RawMessage) (string, error) {
			rec.runs++
			rec.approved = tool.CallApproved(ctx)
			if !tool.CallApproved(ctx) {
				return "", &tool.Deferred{Kind: tool.DeferApproval, Reason: "sign-off"}
			}
			return "deleted", nil
		},
	})
}

func TestRunFailsWhenToolDefersWithoutKind(t *testing.T) {
	t.Parallel()

	kindless := tool.MustNew(tool.Tool[gateDeps]{
		Name:        "delete_file",
		Description: "Defers without a kind.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps gateDeps, args json.RawMessage) (string, error) {
			return "", &tool.Deferred{Reason: "oops"}
		},
	})
	agent, err := golem.New[gateDeps, string](
		&queuedModel{responses: []model.Response{pauseResponse("delete_file")}},
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) { return r.Message.Content, nil }),
		golem.WithTools[gateDeps, string](kindless),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = agent.Run(context.Background(), golem.RunContext[gateDeps]{}, "go")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageTool {
		t.Fatalf("error = %v, want a tool-stage RunError", err)
	}
	if !strings.Contains(err.Error(), "deferred without a kind") {
		t.Fatalf("error = %v, want the missing-kind cause", err)
	}
}

func TestResumeApprovalReExecutesTool(t *testing.T) {
	t.Parallel()

	client, agent, rec := gateClient(t,
		pauseResponse("delete_file"),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "deleted notes.txt"}},
	)

	paused := pausedRun(t, agent)
	result, err := agent.RunWithDeferredResults(context.Background(), golem.RunContext[gateDeps]{}, paused.Messages,
		golem.DeferredResults{Approvals: map[string]golem.Approval{"call-1": {Approved: true}}}, "")
	if err != nil {
		t.Fatalf("RunWithDeferredResults() error = %v", err)
	}
	if rec.runs != 2 || !rec.approved || len(rec.deletions) != 1 {
		t.Fatalf("tool ran %d times (approved=%v, deletions=%v), want one approved re-run",
			rec.runs, rec.approved, rec.deletions)
	}
	if result.Output != "deleted notes.txt" {
		t.Fatalf("output = %q, want the resumed final answer", result.Output)
	}
	if result.Pending != nil {
		t.Fatalf("resumed result is pending: %#v", result.Pending)
	}
	// The resume request carries the executed re-run result after the
	// requesting assistant turn, and no empty user prompt.
	sent := client.requests[1].Messages
	if len(sent) != 3 {
		t.Fatalf("resume sent %d messages, want 3 (user, assistant, re-run result)", len(sent))
	}
	toolResult := sent[2]
	if toolResult.Role != model.RoleTool || toolResult.ToolCallID != "call-1" || toolResult.Content != "deleted" {
		t.Fatalf("re-run result = %#v, want the tool's approved output", toolResult)
	}
}

func TestResumeDenialReachesModelWithoutReExecuting(t *testing.T) {
	t.Parallel()

	client, agent, rec := gateClient(t,
		pauseResponse("delete_file"),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "understood, skipping"}},
	)

	paused := pausedRun(t, agent)
	result, err := agent.RunWithDeferredResults(context.Background(), golem.RunContext[gateDeps]{}, paused.Messages,
		golem.DeferredResults{Approvals: map[string]golem.Approval{"call-1": {Reason: "not on weekends"}}}, "")
	if err != nil {
		t.Fatalf("RunWithDeferredResults() error = %v", err)
	}
	if rec.runs != 1 {
		t.Fatalf("tool ran %d times, want the deferred pass only", rec.runs)
	}
	if result.Output != "understood, skipping" {
		t.Fatalf("output = %q", result.Output)
	}
	sent := client.requests[1].Messages
	denial := sent[len(sent)-1]
	if denial.Role != model.RoleTool || !strings.Contains(denial.Content, "not on weekends") {
		t.Fatalf("denial result = %#v, want the denial reason", denial)
	}
}

func TestResumeExternalResultReachesModelVerbatim(t *testing.T) {
	t.Parallel()

	report := tool.MustNew(tool.Tool[gateDeps]{
		Name:        "delete_file",
		Description: "Start a report build.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps gateDeps, args json.RawMessage) (string, error) {
			return "", &tool.Deferred{Kind: tool.DeferExternal, Reason: "job-7"}
		},
	})
	client := &queuedModel{responses: []model.Response{
		pauseResponse("delete_file"),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "report says 42"}},
	}}
	agent, err := golem.New[gateDeps, string](
		client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) { return r.Message.Content, nil }),
		golem.WithTools[gateDeps, string](report),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	paused, err := agent.Run(context.Background(), golem.RunContext[gateDeps]{}, "build the report")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	result, err := agent.RunWithDeferredResults(context.Background(), golem.RunContext[gateDeps]{}, paused.Messages,
		golem.DeferredResults{External: map[string]string{"call-1": "the report counts 42 widgets"}}, "")
	if err != nil {
		t.Fatalf("RunWithDeferredResults() error = %v", err)
	}
	if result.Output != "report says 42" {
		t.Fatalf("output = %q", result.Output)
	}
	sent := client.requests[1].Messages
	external := sent[len(sent)-1]
	if external.Content != "the report counts 42 widgets" {
		t.Fatalf("external result = %q, want the supplied text verbatim", external.Content)
	}
}

func TestResumeResolutionsInterleaveInEmissionOrder(t *testing.T) {
	t.Parallel()

	rec := &gateRecorder{}
	stat := tool.MustNew(tool.Tool[gateDeps]{
		Name:        "stat_file",
		Description: "Stat a file.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps gateDeps, args json.RawMessage) (string, error) {
			return "size=12", nil
		},
	})
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "delete_file", Args: json.RawMessage(`{"path":"a"}`)},
			{ID: "call-2", Name: "stat_file", Args: json.RawMessage(`{}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	}}
	agent, err := golem.New[gateDeps, string](
		client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) { return r.Message.Content, nil }),
		golem.WithTools[gateDeps, string](mustGatedDeleter(t, rec), stat),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	paused, err := agent.Run(context.Background(), golem.RunContext[gateDeps]{}, "clean up")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if _, err := agent.RunWithDeferredResults(context.Background(), golem.RunContext[gateDeps]{}, paused.Messages,
		golem.DeferredResults{Approvals: map[string]golem.Approval{"call-1": {Approved: true}}}, ""); err != nil {
		t.Fatalf("RunWithDeferredResults() error = %v", err)
	}
	sent := client.requests[1].Messages
	if len(sent) != 4 {
		t.Fatalf("resume sent %d messages, want 4", len(sent))
	}
	if sent[2].ToolCallID != "call-1" || sent[2].Content != "deleted" {
		t.Fatalf("first result = %#v, want the re-run result first (emission order)", sent[2])
	}
	if sent[3].ToolCallID != "call-2" || sent[3].Content != "size=12" {
		t.Fatalf("second result = %#v, want the executed stat result second", sent[3])
	}
}

func TestResumeValidationFailsBeforeAnyModelCall(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		history func(paused []model.Message) []model.Message
		results golem.DeferredResults
		want    string
	}{
		{
			name:    "no pending calls",
			history: func(paused []model.Message) []model.Message { return nil },
			results: golem.DeferredResults{},
			want:    "no pending deferred tool calls",
		},
		{
			name:    "missing resolution",
			history: func(paused []model.Message) []model.Message { return paused },
			results: golem.DeferredResults{},
			want:    `pending tool call "call-1" (delete_file) has no resolution`,
		},
		{
			name:    "unknown call id",
			history: func(paused []model.Message) []model.Message { return paused },
			results: golem.DeferredResults{Approvals: map[string]golem.Approval{"call-9": {Approved: true}}},
			want:    `approval for unknown tool call "call-9"`,
		},
		{
			name:    "resolved in both maps",
			history: func(paused []model.Message) []model.Message { return paused },
			results: golem.DeferredResults{
				Approvals: map[string]golem.Approval{"call-1": {Approved: true}},
				External:  map[string]string{"call-1": "text"},
			},
			want: `both an approval and an external result`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			client, agent, rec := gateClient(t,
				pauseResponse("delete_file"),
				model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "resumed"}},
			)
			paused := pausedRun(t, agent)
			callsBefore := len(client.requests)

			_, err := agent.RunWithDeferredResults(context.Background(), golem.RunContext[gateDeps]{},
				test.history(paused.Messages), test.results, "")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if len(client.requests) != callsBefore {
				t.Fatal("resume validation ran a model call")
			}
			if rec.runs != 1 {
				t.Fatalf("tool ran %d times, want no re-execution on invalid resume", rec.runs)
			}
		})
	}
}

func TestResumeApprovedRerunFailureIsToolStage(t *testing.T) {
	t.Parallel()

	failing := tool.MustNew(tool.Tool[gateDeps]{
		Name:        "delete_file",
		Description: "Delete a file.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps gateDeps, args json.RawMessage) (string, error) {
			if !tool.CallApproved(ctx) {
				return "", &tool.Deferred{Kind: tool.DeferApproval, Reason: "sign-off"}
			}
			return "", errors.New("disk on fire")
		},
	})
	client := &queuedModel{responses: []model.Response{
		pauseResponse("delete_file"),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "unused"}},
	}}
	agent, err := golem.New[gateDeps, string](
		client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) { return r.Message.Content, nil }),
		golem.WithTools[gateDeps, string](failing),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	paused, err := agent.Run(context.Background(), golem.RunContext[gateDeps]{}, "delete notes.txt")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	_, err = agent.RunWithDeferredResults(context.Background(), golem.RunContext[gateDeps]{}, paused.Messages,
		golem.DeferredResults{Approvals: map[string]golem.Approval{"call-1": {Approved: true}}}, "")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageTool {
		t.Fatalf("error = %v, want a tool-stage RunError", err)
	}
	if !strings.Contains(err.Error(), "approved re-run failed") || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("error = %v, want the re-run cause preserved", err)
	}
}

func TestResumeApprovedRerunDeferringAgainFails(t *testing.T) {
	t.Parallel()

	relapsing := tool.MustNew(tool.Tool[gateDeps]{
		Name:        "delete_file",
		Description: "Delete a file.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps gateDeps, args json.RawMessage) (string, error) {
			return "", &tool.Deferred{Kind: tool.DeferApproval, Reason: "again"}
		},
	})
	client := &queuedModel{responses: []model.Response{
		pauseResponse("delete_file"),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "unused"}},
	}}
	agent, err := golem.New[gateDeps, string](
		client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) { return r.Message.Content, nil }),
		golem.WithTools[gateDeps, string](relapsing),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	paused, err := agent.Run(context.Background(), golem.RunContext[gateDeps]{}, "delete notes.txt")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	_, err = agent.RunWithDeferredResults(context.Background(), golem.RunContext[gateDeps]{}, paused.Messages,
		golem.DeferredResults{Approvals: map[string]golem.Approval{"call-1": {Approved: true}}}, "")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageTool {
		t.Fatalf("error = %v, want a tool-stage RunError", err)
	}
	if !strings.Contains(err.Error(), "deferred") {
		t.Fatalf("error = %v, want the repeated deferral as cause", err)
	}
}

func TestResumeUnregisteredToolFailsBeforeModelCall(t *testing.T) {
	t.Parallel()

	_, agent, _ := gateClient(t,
		pauseResponse("delete_file"),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "unused"}},
	)
	paused := pausedRun(t, agent)

	// A fresh agent without the tool registered cannot resolve the
	// approval — the failure comes before any model call.
	empty, err := golem.New[gateDeps, string](
		&queuedModel{responses: []model.Response{
			model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "unused"}},
		}},
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) { return r.Message.Content, nil }),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = empty.RunWithDeferredResults(context.Background(), golem.RunContext[gateDeps]{}, paused.Messages,
		golem.DeferredResults{Approvals: map[string]golem.Approval{"call-1": {Approved: true}}}, "")
	if err == nil || !strings.Contains(err.Error(), `tool "delete_file" is not registered`) {
		t.Fatalf("error = %v, want the unregistered-tool cause", err)
	}
}

func TestResumeWithEmptyPromptOmitsUserMessage(t *testing.T) {
	t.Parallel()

	client, agent, _ := gateClient(t,
		pauseResponse("delete_file"),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "resumed"}},
	)
	paused := pausedRun(t, agent)

	if _, err := agent.RunWithDeferredResults(context.Background(), golem.RunContext[gateDeps]{}, paused.Messages,
		golem.DeferredResults{Approvals: map[string]golem.Approval{"call-1": {Approved: false}}}, ""); err != nil {
		t.Fatalf("RunWithDeferredResults() error = %v", err)
	}
	sent := client.requests[1].Messages
	if len(sent) != 3 {
		t.Fatalf("resume sent %d messages, want 3 with no empty user prompt", len(sent))
	}
	// The conversation re-opens on the resolutions alone: the last
	// message is the denial, not a fresh user turn.
	last := sent[len(sent)-1]
	if last.Role != model.RoleTool || !strings.Contains(last.Content, "denied") {
		t.Fatalf("last message = %#v, want the denial closing the conversation", last)
	}
}

func TestResumeWithNewPromptAppendsUserMessage(t *testing.T) {
	t.Parallel()

	client, agent, _ := gateClient(t,
		pauseResponse("delete_file"),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "resumed"}},
	)
	paused := pausedRun(t, agent)

	if _, err := agent.RunWithDeferredResults(context.Background(), golem.RunContext[gateDeps]{}, paused.Messages,
		golem.DeferredResults{Approvals: map[string]golem.Approval{"call-1": {Approved: false}}},
		"and back it up first"); err != nil {
		t.Fatalf("RunWithDeferredResults() error = %v", err)
	}
	sent := client.requests[1].Messages
	prompt := sent[len(sent)-1]
	if prompt.Role != model.RoleUser || prompt.Content != "and back it up first" {
		t.Fatalf("last message = %#v, want the new prompt last", prompt)
	}
}

func TestRunStreamPausesOnDeferredTool(t *testing.T) {
	t.Parallel()

	client := &streamQueuedModel{queuedModel{responses: []model.Response{
		pauseResponse("delete_file"),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "resumed"}},
	}}}
	agent, err := golem.New[gateDeps, string](
		client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) { return r.Message.Content, nil }),
		golem.WithTools[gateDeps, string](mustGatedDeleter(t, &gateRecorder{})),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.RunStream(context.Background(), golem.RunContext[gateDeps]{}, "delete notes.txt",
		func(model.Delta) error { return nil })
	if err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}
	if result.Pending == nil || len(result.Pending.Approvals) != 1 {
		t.Fatalf("pending = %#v, want the deferred call", result.Pending)
	}

	resumed, err := agent.RunWithDeferredResults(context.Background(), golem.RunContext[gateDeps]{}, result.Messages,
		golem.DeferredResults{Approvals: map[string]golem.Approval{"call-1": {Approved: false, Reason: "no"}}}, "")
	if err != nil {
		t.Fatalf("RunWithDeferredResults() error = %v", err)
	}
	if resumed.Output != "resumed" {
		t.Fatalf("output = %q", resumed.Output)
	}
}
