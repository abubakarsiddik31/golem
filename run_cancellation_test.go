package golem_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

// cancelResponse is the assistant turn requesting the named calls.
func cancelResponse(calls ...model.ToolCall) model.Response {
	return model.Response{
		Message: model.Message{Role: model.RoleAssistant, ToolCalls: calls},
		Usage:   model.Usage{InputTokens: 9, OutputTokens: 2},
	}
}

func cancelCall(id, name string) model.ToolCall {
	return model.ToolCall{ID: id, Name: name, Args: json.RawMessage(`{}`)}
}

func trackedTool(name string, runs *int, exec func() (tool.Result, error)) tool.Tool[struct{}] {
	return tool.MustNew(tool.Tool[struct{}]{
		Name: name, Description: "Counted execution.", Schema: json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (tool.Result, error) {
			*runs++
			return exec()
		},
	})
}

// A tool's cancellation sentinel ends the run at the cancellation stage:
// the sentinel stays reachable through the chain, and the partial
// evidence closes the unanswered call so the transcript stays resumable.
func TestToolCancelEndsRunCleanly(t *testing.T) {
	t.Parallel()

	client := testmodel.New().Respond(cancelResponse(cancelCall("call-1", "stopper")))
	stopper := tool.MustNew(tool.Tool[struct{}]{
		Name: "stopper", Description: "Always stops the run.", Schema: json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (tool.Result, error) {
			return tool.Result{}, &tool.Canceled{Reason: "policy veto"}
		},
	})
	agent, err := golem.New[struct{}, string](client, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](stopper))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var stopped *tool.Canceled
	if !errors.As(err, &stopped) || stopped.Reason != "policy veto" {
		t.Fatalf("Run() error = %v, want the sentinel reachable with its reason", err)
	}
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageCanceled {
		t.Fatalf("Run() error = %v, want cancellation stage", err)
	}
	partial := runErr.Partial
	if partial == nil {
		t.Fatal("RunError.Partial = nil, want the evidence preserved")
	}
	// user, assistant call, synthesized closing of the unanswered call.
	if len(partial.Messages) != 3 {
		t.Fatalf("partial messages = %#v, want user, assistant call, closing", partial.Messages)
	}
	closing := partial.Messages[2]
	if closing.Role != model.RoleTool || closing.ToolCallID != "call-1" ||
		!strings.Contains(closing.Content, "no result was produced") {
		t.Fatalf("closing = %#v, want the synthesized no-result result", closing)
	}
	if closing.RunID == "" || closing.ConversationID == "" {
		t.Fatalf("closing = %#v, want the run's identity stamped", closing)
	}
	if partial.Usage.InputTokens != 9 {
		t.Fatalf("partial usage = %+v, want the completed turn's usage", partial.Usage)
	}
	if partial.Requests != 1 || partial.ToolCalls != 1 {
		t.Fatalf("partial counts = (%d requests, %d tool calls), want (1, 1)", partial.Requests, partial.ToolCalls)
	}
}

// A stop ends the batch in emission order: earlier results stay, later
// groups never start, and every unanswered call is closed.
func TestToolCancelKeepsEarlierResultsAndSkipsLaterGroups(t *testing.T) {
	t.Parallel()

	var earlyRuns, lateRuns int
	client := testmodel.New().Respond(cancelResponse(
		cancelCall("call-1", "early"),
		cancelCall("call-2", "stopper"),
		cancelCall("call-3", "late"),
	))
	early := trackedTool("early", &earlyRuns, func() (tool.Result, error) { return tool.Text("early done"), nil })
	late := trackedTool("late", &lateRuns, func() (tool.Result, error) { return tool.Text("late done"), nil })
	stopper := tool.MustNew(tool.Tool[struct{}]{
		Name: "stopper", Description: "Stops the run.", Schema: json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (tool.Result, error) {
			return tool.Result{}, &tool.Canceled{Reason: "enough"}
		},
	})
	agent, err := golem.New[struct{}, string](client, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](early, stopper, late))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageCanceled {
		t.Fatalf("Run() error = %v, want cancellation stage", err)
	}
	if earlyRuns != 1 || lateRuns != 0 {
		t.Fatalf("tool runs = (early %d, late %d), want the stop to prevent the later group", earlyRuns, lateRuns)
	}
	partial := runErr.Partial
	if partial == nil || len(partial.Messages) != 5 {
		t.Fatalf("partial messages = %#v, want assistant turn plus three closings/results", partial.Messages)
	}
	if partial.Messages[2].Content != "early done" {
		t.Fatalf("first result = %q, want the earlier call's recorded result", partial.Messages[2].Content)
	}
	if partial.Messages[3].ToolCallID != "call-2" || partial.Messages[4].ToolCallID != "call-3" {
		t.Fatalf("closings = %#v, want calls 2 and 3 closed in emission order", partial.Messages[3:])
	}
	if partial.Requests != 1 || partial.ToolCalls != 2 {
		t.Fatalf("partial counts = (%d, %d), want (1, 2): the stopped call executed, the skipped one did not",
			partial.Requests, partial.ToolCalls)
	}
}

// Inside one parallel group every call executes concurrently, so the
// siblings' side effects happen; their results are still discarded —
// the stop closes them with the synthesized no-result message.
func TestToolCancelInParallelGroupDiscardsSiblingResults(t *testing.T) {
	t.Parallel()

	var siblingRuns int
	client := testmodel.New().Respond(cancelResponse(
		cancelCall("call-1", "stopper"),
		cancelCall("call-2", "sibling"),
	))
	sibling := trackedTool("sibling", &siblingRuns, func() (tool.Result, error) { return tool.Text("sibling done"), nil })
	stopper := tool.MustNew(tool.Tool[struct{}]{
		Name: "stopper", Description: "Stops the run.", Schema: json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (tool.Result, error) {
			return tool.Result{}, &tool.Canceled{Reason: "stop"}
		},
	})
	agent, err := golem.New[struct{}, string](client, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](stopper, sibling), golem.WithParallelToolCalls[struct{}, string]())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageCanceled {
		t.Fatalf("Run() error = %v, want cancellation stage", err)
	}
	if siblingRuns != 1 {
		t.Fatalf("sibling runs = %d, want the concurrent sibling to have executed", siblingRuns)
	}
	messages := runErr.Partial.Messages
	if len(messages) != 4 ||
		!strings.Contains(messages[2].Content, "no result was produced") ||
		!strings.Contains(messages[3].Content, "no result was produced") {
		t.Fatalf("closings = %#v, want both calls closed with the no-result message", messages[2:])
	}
	if messages[2].ToolCallID != "call-1" || messages[3].ToolCallID != "call-2" {
		t.Fatalf("closings = %#v, want emission order", messages[2:])
	}
	if runErr.Partial.ToolCalls != 2 {
		t.Fatalf("partial tool calls = %d, want both executions counted", runErr.Partial.ToolCalls)
	}
}

// The observer sees the cancelling call's tool end carrying the sentinel,
// then EventCanceled; calls the stop prevented never emit.
func TestToolCancelEmitsDeterministicEvents(t *testing.T) {
	t.Parallel()

	client := testmodel.New().Respond(cancelResponse(cancelCall("call-1", "stopper")))
	stopper := tool.MustNew(tool.Tool[struct{}]{
		Name: "stopper", Description: "Stops the run.", Schema: json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (tool.Result, error) {
			return tool.Result{}, &tool.Canceled{Reason: "stop"}
		},
	})
	agent, err := golem.New[struct{}, string](client, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](stopper))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var kinds []golem.EventKind
	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go",
		golem.WithRunObserver(func(event golem.RunEvent) { kinds = append(kinds, event.Kind) }))
	var stopped *tool.Canceled
	if !errors.As(err, &stopped) {
		t.Fatalf("Run() error = %v, want the sentinel", err)
	}
	want := []golem.EventKind{
		golem.EventModelStart, golem.EventModelEnd,
		golem.EventToolStart, golem.EventToolEnd, golem.EventCanceled,
	}
	if len(kinds) != len(want) {
		t.Fatalf("events = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("events = %v, want %v", kinds, want)
		}
	}
}

// Streaming runs end the same way as plain runs.
func TestToolCancelStreamParity(t *testing.T) {
	t.Parallel()

	client := testmodel.New().Respond(cancelResponse(cancelCall("call-1", "stopper")))
	stopper := tool.MustNew(tool.Tool[struct{}]{
		Name: "stopper", Description: "Stops the run.", Schema: json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (tool.Result, error) {
			return tool.Result{}, &tool.Canceled{Reason: "stream stop"}
		},
	})
	agent, err := golem.New[struct{}, string](client, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](stopper))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.RunStream(context.Background(), golem.RunContext[struct{}]{}, "go", nil)
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageCanceled {
		t.Fatalf("RunStream() error = %v, want cancellation stage", err)
	}
	if len(runErr.Partial.Messages) != 3 {
		t.Fatalf("partial messages = %#v, want the closing kept in streaming too", runErr.Partial.Messages)
	}
}

// The canceled transcript is already provider-valid: resuming it needs no
// repair, and the model sees the synthesized closings.
func TestCanceledPartialResumesWithoutRepair(t *testing.T) {
	t.Parallel()

	client := testmodel.New().Respond(
		cancelResponse(cancelCall("call-1", "stopper")),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "resumed"}},
	)
	stopper := tool.MustNew(tool.Tool[struct{}]{
		Name: "stopper", Description: "Stops the run.", Schema: json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (tool.Result, error) {
			return tool.Result{}, &tool.Canceled{Reason: "stop"}
		},
	})
	agent, err := golem.New[struct{}, string](client, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](stopper))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, runErr := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var stop *golem.RunError
	if !errors.As(runErr, &stop) {
		t.Fatalf("Run() error = %v, want a RunError", runErr)
	}
	_, repair := golem.NormalizeHistory(stop.Partial.Messages)
	if len(repair.Synthesized) != 0 || len(repair.Dropped) != 0 {
		t.Fatalf("repair report = %+v, want the canceled transcript already paired", repair)
	}

	result, err := agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{}, stop.Partial.Messages, "continue")
	if err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}
	if result.Output != "resumed" {
		t.Fatalf("output = %q, want the resumed answer", result.Output)
	}
}

// A delegated sub-agent's stop cancels the delegating run at the same
// stage, sentinel still reachable.
func TestSubAgentCancelPropagatesToParent(t *testing.T) {
	t.Parallel()

	subClient := testmodel.New().Respond(cancelResponse(cancelCall("inner-1", "sub-stopper")))
	subStopper := tool.MustNew(tool.Tool[struct{}]{
		Name: "sub-stopper", Description: "Stops the sub-run.", Schema: json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (tool.Result, error) {
			return tool.Result{}, &tool.Canceled{Reason: "inner veto"}
		},
	})
	sub, err := golem.New[struct{}, string](subClient, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](subStopper))
	if err != nil {
		t.Fatalf("New() sub error = %v", err)
	}
	delegated, err := sub.AsTool("researcher", "Delegate.")
	if err != nil {
		t.Fatalf("AsTool() error = %v", err)
	}
	parentClient := testmodel.New().Respond(model.Response{
		Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "researcher", Args: json.RawMessage(`{"prompt":"go"}`)},
		}},
	})
	parent, err := golem.New[struct{}, string](parentClient, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](delegated))
	if err != nil {
		t.Fatalf("New() parent error = %v", err)
	}

	_, err = parent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageCanceled {
		t.Fatalf("Run() error = %v, want the parent canceled at the cancellation stage", err)
	}
	var stopped *tool.Canceled
	if !errors.As(err, &stopped) || stopped.Reason != "inner veto" {
		t.Fatalf("Run() error = %v, want the inner sentinel reachable", err)
	}
}

// An approved deferred re-run that returns the sentinel ends the resume
// run at the cancellation stage before any model call.
func TestApprovedRerunCancelEndsResume(t *testing.T) {
	t.Parallel()

	var reruns int
	client := testmodel.New().Respond(
		cancelResponse(cancelCall("call-1", "delete_file")),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	)
	deleteFile := tool.MustNew(tool.Tool[struct{}]{
		Name: "delete_file", Description: "Delete a file.", Schema: json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (tool.Result, error) {
			reruns++
			if !tool.CallApproved(ctx) {
				return tool.Result{}, &tool.Deferred{Kind: tool.DeferApproval, Reason: "sign-off"}
			}
			return tool.Result{}, &tool.Canceled{Reason: "vetoed at execution"}
		},
	})
	agent, err := golem.New[struct{}, string](client, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](deleteFile))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	paused, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	if err != nil || paused.Pending == nil {
		t.Fatalf("Run() = (%v, %v), want a paused run", paused, err)
	}

	_, err = agent.RunWithDeferredResults(context.Background(), golem.RunContext[struct{}]{}, paused.Messages,
		golem.DeferredResults{Approvals: map[string]golem.Approval{"call-1": {Approved: true}}}, "")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageCanceled {
		t.Fatalf("RunWithDeferredResults() error = %v, want cancellation stage", err)
	}
	if runErr.Partial != nil {
		t.Fatalf("partial = %+v, want nil: the resume ended before any model call", runErr.Partial)
	}
	if reruns != 2 {
		t.Fatalf("tool ran %d times, want the pause plus the approved re-run", reruns)
	}
}

// A stop discards a pending pause: a batch that defers and then cancels
// ends canceled, with every call closed and nothing left pending.
func TestToolCancelDiscardsPendingPause(t *testing.T) {
	t.Parallel()

	client := testmodel.New().Respond(cancelResponse(
		cancelCall("call-1", "deferer"),
		cancelCall("call-2", "stopper"),
	))
	deferer := tool.MustNew(tool.Tool[struct{}]{
		Name: "deferer", Description: "Always defers.", Schema: json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (tool.Result, error) {
			return tool.Result{}, &tool.Deferred{Kind: tool.DeferApproval, Reason: "sign-off"}
		},
	})
	stopper := tool.MustNew(tool.Tool[struct{}]{
		Name: "stopper", Description: "Stops the run.", Schema: json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (tool.Result, error) {
			return tool.Result{}, &tool.Canceled{Reason: "stop"}
		},
	})
	agent, err := golem.New[struct{}, string](client, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](deferer, stopper))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageCanceled {
		t.Fatalf("Run() error = %v, want cancellation stage", err)
	}
	if result.Pending != nil {
		t.Fatalf("pending = %+v, want the pause discarded", result.Pending)
	}
	messages := runErr.Partial.Messages
	if len(messages) != 4 {
		t.Fatalf("partial messages = %#v, want both calls closed", messages)
	}
	if messages[2].ToolCallID != "call-1" || messages[3].ToolCallID != "call-2" {
		t.Fatalf("closings = %#v, want emission order", messages[2:])
	}
}
