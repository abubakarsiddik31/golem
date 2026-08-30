package golem_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

// retryableFailure reports itself retryable so model-call retries engage.
type retryableFailure struct{ err error }

func (e *retryableFailure) Error() string { return e.err.Error() }
func (e *retryableFailure) Retryable() bool {
	return true
}
func (e *retryableFailure) Unwrap() error { return e.err }

// summarize renders the observable identity of an event for sequence
// assertions: kind, turn, attempt, and call ID.
func summarize(events []golem.RunEvent) []string {
	summaries := make([]string, len(events))
	for i, event := range events {
		summaries[i] = fmt.Sprintf("%s t%d a%d %s", event.Kind, event.Turn, event.Attempt, event.CallID)
	}
	return summaries
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestRunEventsCoverTwoTurnToolExchange(t *testing.T) {
	t.Parallel()

	lookup := tool.MustNew(tool.Tool[struct{}]{
		Name:        "lookup",
		Description: "Looks a value up.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (string, error) {
			return "42", nil
		},
	})
	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "c1", Name: "lookup", Args: json.RawMessage(`{}`)},
		}}, Usage: model.Usage{InputTokens: 3, OutputTokens: 1}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "done"}, Usage: model.Usage{InputTokens: 5, OutputTokens: 2}},
	)
	var events []golem.RunEvent
	agent, err := golem.New[struct{}, string](
		m,
		golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](lookup),
		golem.WithRunEvents[struct{}, string](func(event golem.RunEvent) {
			events = append(events, event)
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "done" {
		t.Fatalf("Output = %q", result.Output)
	}

	want := []string{
		"model_start t0 a1 ", "model_end t0 a1 ",
		"tool_start t0 a0 c1", "tool_end t0 a0 c1",
		"model_start t1 a1 ", "model_end t1 a1 ",
	}
	if got := summarize(events); !equalStrings(got, want) {
		t.Fatalf("event sequence = %v, want %v", got, want)
	}
	// Attempt usage and tool arguments travel on their events.
	if events[1].Usage != (model.Usage{InputTokens: 3, OutputTokens: 1}) || events[5].Usage != (model.Usage{InputTokens: 5, OutputTokens: 2}) {
		t.Fatalf("model_end usage = %v, %v", events[1].Usage, events[5].Usage)
	}
	if events[2].ToolName != "lookup" || string(events[2].Args) != `{}` {
		t.Fatalf("tool_start = %+v", events[2])
	}
	if events[3].Result != "42" || events[3].Err != nil {
		t.Fatalf("tool_end = %+v", events[3])
	}
}

func TestRunEventsExposeRetriedAttempts(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("transient")
	m := testmodel.New().Fail(
		&retryableFailure{err: sentinel},
	).Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "ok"}},
	)
	var events []golem.RunEvent
	agent, err := golem.New[struct{}, string](
		m,
		golem.DecodeFunc[string](decodeContent),
		golem.WithMaxAttempts[struct{}, string](2),
		golem.WithRetryBackoff[struct{}, string](func(int) time.Duration { return 0 }),
		golem.WithRunEvents[struct{}, string](func(event golem.RunEvent) {
			events = append(events, event)
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := []string{
		"model_start t0 a1 ", "model_end t0 a1 ",
		"model_start t0 a2 ", "model_end t0 a2 ",
	}
	if got := summarize(events); !equalStrings(got, want) {
		t.Fatalf("event sequence = %v, want %v", got, want)
	}
	if !errors.Is(events[1].Err, sentinel) {
		t.Fatalf("failed attempt error chain = %v", events[1].Err)
	}
	if events[3].Err != nil {
		t.Fatalf("successful attempt end carries error %v", events[3].Err)
	}
}

func TestRunEventsCarryToolRejections(t *testing.T) {
	t.Parallel()

	strict := tool.MustNew(tool.Tool[struct{}]{
		Name:        "strict",
		Description: "Rejects bad arguments correctably.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (string, error) {
			return "", &model.ModelRetry{Err: errors.New("arguments must name a city")}
		},
	})
	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "c1", Name: "strict", Args: json.RawMessage(`{}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "gave up gracefully"}},
	)
	var events []golem.RunEvent
	agent, err := golem.New[struct{}, string](
		m,
		golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](strict),
		golem.WithToolRetries[struct{}, string](1),
		golem.WithRunEvents[struct{}, string](func(event golem.RunEvent) {
			events = append(events, event)
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var rejection *model.ModelRetry
	found := false
	for _, event := range events {
		if event.Kind == golem.EventToolEnd {
			if errors.As(event.Err, &rejection) {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("no tool_end carried a ModelRetry rejection: %v", summarize(events))
	}
}

func TestRunEventsParallelGroupsStayOrdered(t *testing.T) {
	t.Parallel()

	first := tool.MustNew(tool.Tool[struct{}]{
		Name: "first", Schema: json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (string, error) {
			return "one", nil
		},
	})
	second := tool.MustNew(tool.Tool[struct{}]{
		Name: "second", Schema: json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (string, error) {
			return "two", nil
		},
	})
	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "c1", Name: "first", Args: json.RawMessage(`{}`)},
			{ID: "c2", Name: "second", Args: json.RawMessage(`{}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	)
	var events []golem.RunEvent
	agent, err := golem.New[struct{}, string](
		m,
		golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](first, second),
		golem.WithParallelToolCalls[struct{}, string](),
		golem.WithRunEvents[struct{}, string](func(event golem.RunEvent) {
			events = append(events, event)
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := []string{
		"model_start t0 a1 ", "model_end t0 a1 ",
		"tool_start t0 a0 c1", "tool_start t0 a0 c2",
		"tool_end t0 a0 c1", "tool_end t0 a0 c2",
		"model_start t1 a1 ", "model_end t1 a1 ",
	}
	if got := summarize(events); !equalStrings(got, want) {
		t.Fatalf("event sequence = %v, want %v", got, want)
	}
}

func TestRunEventsMarkOutputCorrectionRounds(t *testing.T) {
	t.Parallel()

	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "seven"}, Usage: model.Usage{InputTokens: 2, OutputTokens: 1}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "7"}, Usage: model.Usage{InputTokens: 2, OutputTokens: 1}},
	)
	calls := 0
	decoder := golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
		calls++
		if calls == 1 {
			return "", &model.ModelRetry{Err: errors.New("want a digit")}
		}
		return r.Message.Content, nil
	})
	var events []golem.RunEvent
	agent, err := golem.New[struct{}, string](
		m,
		decoder,
		golem.WithOutputRetries[struct{}, string](1),
		golem.WithRunEvents[struct{}, string](func(event golem.RunEvent) {
			events = append(events, event)
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := []string{
		"model_start t0 a1 ", "model_end t0 a1 ",
		"output_rejected t0 a1 ",
		// The correction round restarts turn numbering.
		"model_start t0 a1 ", "model_end t0 a1 ",
	}
	if got := summarize(events); !equalStrings(got, want) {
		t.Fatalf("event sequence = %v, want %v", got, want)
	}
	var rejection *model.ModelRetry
	if !errors.As(events[2].Err, &rejection) || rejection.Err == nil {
		t.Fatalf("output_rejected error = %v, want the ModelRetry", events[2].Err)
	}
}

func TestRunStreamEmitsTheSameEvents(t *testing.T) {
	t.Parallel()

	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "done"}, Usage: model.Usage{InputTokens: 1, OutputTokens: 1}},
	)
	var events []golem.RunEvent
	agent, err := golem.New[struct{}, string](
		m,
		golem.DecodeFunc[string](decodeContent),
		golem.WithRunEvents[struct{}, string](func(event golem.RunEvent) {
			events = append(events, event)
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := agent.RunStream(context.Background(), golem.RunContext[struct{}]{}, "go", nil); err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}

	want := []string{"model_start t0 a1 ", "model_end t0 a1 "}
	if got := summarize(events); !equalStrings(got, want) {
		t.Fatalf("event sequence = %v, want %v", got, want)
	}
	if events[1].Usage != (model.Usage{InputTokens: 1, OutputTokens: 1}) {
		t.Fatalf("model_end usage = %v", events[1].Usage)
	}
}

// A run-scoped observer routes one run's events without rebuilding the
// agent: two runs through a shared agent each see only their own.
func TestRunObserverRoutesEventsPerRun(t *testing.T) {
	t.Parallel()

	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "first"}, Usage: model.Usage{InputTokens: 3}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "second"}, Usage: model.Usage{InputTokens: 7}},
	)
	agent, err := golem.New[struct{}, string](m, golem.DecodeFunc[string](decodeContent))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var first, second []golem.RunEvent
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "one",
		golem.WithRunObserver(func(event golem.RunEvent) { first = append(first, event) })); err != nil {
		t.Fatalf("Run(one) error = %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "two",
		golem.WithRunObserver(func(event golem.RunEvent) { second = append(second, event) })); err != nil {
		t.Fatalf("Run(two) error = %v", err)
	}

	wantOneTurn := []string{"model_start t0 a1 ", "model_end t0 a1 "}
	if got := summarize(first); !equalStrings(got, wantOneTurn) {
		t.Fatalf("first run events = %v, want %v", got, wantOneTurn)
	}
	if got := summarize(second); !equalStrings(got, wantOneTurn) {
		t.Fatalf("second run events = %v, want %v", got, wantOneTurn)
	}
	if first[1].Usage.InputTokens != 3 || second[1].Usage.InputTokens != 7 {
		t.Fatalf("model_end usage = %d, %d; each observer must see its own run's usage",
			first[1].Usage.InputTokens, second[1].Usage.InputTokens)
	}
}

// The run observer composes with the agent's: the construction-scoped
// observer fires first, then the run's, per event.
func TestRunObserverComposesWithAgentObserver(t *testing.T) {
	t.Parallel()

	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	)
	var order []string
	agent, err := golem.New[struct{}, string](m, golem.DecodeFunc[string](decodeContent),
		golem.WithRunEvents[struct{}, string](func(event golem.RunEvent) {
			order = append(order, "agent:"+string(event.Kind))
		}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go",
		golem.WithRunObserver(func(event golem.RunEvent) {
			order = append(order, "run:"+string(event.Kind))
		})); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := []string{
		"agent:model_start", "run:model_start",
		"agent:model_end", "run:model_end",
	}
	if !equalStrings(order, want) {
		t.Fatalf("observer order = %v, want %v", order, want)
	}
}

// A nil run observer is a no-op, matching a nil onDelta.
func TestRunObserverNilIsNoOp(t *testing.T) {
	t.Parallel()

	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	)
	agent, err := golem.New[struct{}, string](m, golem.DecodeFunc[string](decodeContent))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go",
		golem.WithRunObserver(nil)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

// Streamed runs route events to the run observer like unstreamed ones.
func TestRunObserverWorksOnStreamRuns(t *testing.T) {
	t.Parallel()

	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "done"}, Usage: model.Usage{InputTokens: 4, OutputTokens: 2}},
	)
	agent, err := golem.New[struct{}, string](m, golem.DecodeFunc[string](decodeContent))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var events []golem.RunEvent
	if _, err := agent.RunStream(context.Background(), golem.RunContext[struct{}]{}, "go", nil,
		golem.WithRunObserver(func(event golem.RunEvent) { events = append(events, event) })); err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}

	want := []string{"model_start t0 a1 ", "model_end t0 a1 "}
	if got := summarize(events); !equalStrings(got, want) {
		t.Fatalf("streamed run events = %v, want %v", got, want)
	}
	if events[1].Usage != (model.Usage{InputTokens: 4, OutputTokens: 2}) {
		t.Fatalf("model_end usage = %v", events[1].Usage)
	}
}

// A deferred resume routes its events to the run observer too: the
// pause emits to the pausing run's observer, the resume to the
// resuming run's.
func TestRunObserverWorksOnDeferredResume(t *testing.T) {
	t.Parallel()

	gated := tool.MustNew(tool.Tool[struct{}]{
		Name: "gated", Description: "Needs sign-off.", Schema: json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (string, error) {
			if tool.CallApproved(ctx) {
				return "signed off", nil
			}
			return "", &tool.Deferred{Kind: tool.DeferApproval, Reason: "sign-off"}
		},
	})
	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "c1", Name: "gated", Args: json.RawMessage(`{}`)},
		}}},
	)
	agent, err := golem.New[struct{}, string](m, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](gated))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var pauseEvents []golem.RunEvent
	paused, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go",
		golem.WithRunObserver(func(event golem.RunEvent) { pauseEvents = append(pauseEvents, event) }))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if paused.Pending == nil {
		t.Fatal("run did not pause for approval")
	}
	wantPause := []string{
		"model_start t0 a1 ", "model_end t0 a1 ",
		"tool_start t0 a0 c1", "deferred t0 a0 c1",
	}
	if got := summarize(pauseEvents); !equalStrings(got, wantPause) {
		t.Fatalf("pause events = %v, want %v", got, wantPause)
	}

	resumed := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "approved and done"}},
	)
	resumeAgent, err := golem.New[struct{}, string](resumed, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](gated))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	var resumeEvents []golem.RunEvent
	_, err = resumeAgent.RunWithDeferredResults(context.Background(), golem.RunContext[struct{}]{},
		paused.Messages, golem.DeferredResults{Approvals: map[string]golem.Approval{
			"c1": {Approved: true},
		}}, "",
		golem.WithRunObserver(func(event golem.RunEvent) { resumeEvents = append(resumeEvents, event) }))
	if err != nil {
		t.Fatalf("RunWithDeferredResults() error = %v", err)
	}
	wantResume := []string{"model_start t0 a1 ", "model_end t0 a1 "}
	if got := summarize(resumeEvents); !equalStrings(got, wantResume) {
		t.Fatalf("resume events = %v, want %v", got, wantResume)
	}
}
