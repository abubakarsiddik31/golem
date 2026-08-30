package golem_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

// errProviderGone is the sentinel model failures in these tests raise.
var (
	errProviderGone  = errors.New("provider gone")
	errMidCorrection = errors.New("provider gone mid-correction")
)

func echoToolFor(deps struct{}) tool.Tool[struct{}] {
	return tool.MustNew(tool.Tool[struct{}]{
		Name:        "echo",
		Description: "Echo a fixed reply.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (string, error) {
			return "echoed", nil
		},
	})
}

// A model failure after a completed tool turn keeps the whole turn as
// partial evidence: messages, usage, and activity counts.
func TestModelFailureKeepsPartialEvidence(t *testing.T) {
	t.Parallel()

	client := testmodel.New().Respond(
		model.Response{
			Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "call-1", Name: "echo", Args: json.RawMessage(`{}`)},
			}},
			Usage: model.Usage{InputTokens: 11, OutputTokens: 4},
		},
	).Fail(errProviderGone)

	agent, err := golem.New[struct{}, string](client, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](echoToolFor(struct{}{})))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	if !errors.Is(err, errProviderGone) {
		t.Fatalf("Run() error = %v, want the provider cause preserved", err)
	}
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageModel {
		t.Fatalf("Run() error = %v, want model stage", err)
	}

	partial := runErr.Partial
	if partial == nil {
		t.Fatal("RunError.Partial = nil, want the completed tool turn preserved")
	}
	if len(partial.Messages) != 3 ||
		partial.Messages[0].Role != model.RoleUser ||
		partial.Messages[1].Role != model.RoleAssistant ||
		len(partial.Messages[1].ToolCalls) != 1 ||
		partial.Messages[2].Role != model.RoleTool {
		t.Fatalf("partial messages = %#v, want user, assistant call, tool result", partial.Messages)
	}
	if partial.Usage != (model.Usage{InputTokens: 11, OutputTokens: 4}) {
		t.Fatalf("partial usage = %+v, want the completed turn's usage", partial.Usage)
	}
	if partial.Requests != 2 {
		t.Fatalf("partial requests = %d, want 2 (completed turn + failed attempt)", partial.Requests)
	}
	if partial.ToolCalls != 1 {
		t.Fatalf("partial tool calls = %d, want 1", partial.ToolCalls)
	}
}

// A tool failure keeps evidence through the assistant turn that
// requested the batch; the batch itself contributes no results.
func TestToolFailureKeepsPartialEvidence(t *testing.T) {
	t.Parallel()

	client := testmodel.New().Respond(
		model.Response{
			Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "call-1", Name: "explode", Args: json.RawMessage(`{}`)},
			}},
			Usage: model.Usage{InputTokens: 7, OutputTokens: 3},
		},
	)
	explode := tool.MustNew(tool.Tool[struct{}]{
		Name: "explode", Description: "Always fails.", Schema: json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (string, error) {
			return "", errors.New("tool exploded")
		},
	})

	agent, err := golem.New[struct{}, string](client, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](explode))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageTool {
		t.Fatalf("Run() error = %v, want tool stage", err)
	}

	partial := runErr.Partial
	if partial == nil {
		t.Fatal("RunError.Partial = nil, want evidence through the assistant call turn")
	}
	if len(partial.Messages) != 2 || partial.Messages[1].Role != model.RoleAssistant {
		t.Fatalf("partial messages = %#v, want user then assistant; the failed batch adds nothing", partial.Messages)
	}
	if partial.Requests != 1 || partial.ToolCalls != 1 {
		t.Fatalf("partial counts = (%d requests, %d tools), want (1, 1)", partial.Requests, partial.ToolCalls)
	}
}

// A failure before any activity — the first model call fails — carries
// no partial evidence.
func TestFailureWithoutEvidenceLeavesPartialNil(t *testing.T) {
	t.Parallel()

	client := testmodel.New().Fail(errors.New("nothing ran"))
	agent, err := golem.New[struct{}, string](client, golem.DecodeFunc[string](decodeContent))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("Run() error = %v, want RunError", err)
	}
	if runErr.Partial != nil {
		t.Fatalf("RunError.Partial = %+v, want nil for a run with no activity", runErr.Partial)
	}
}

// Cancellation mid-run wraps in RunError — the only way its evidence
// survives — while errors.Is keeps matching the context error.
func TestCancellationKeepsPartialEvidence(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testmodel.New().Respond(
		model.Response{
			Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "call-1", Name: "echo", Args: json.RawMessage(`{}`)},
			}},
			Usage: model.Usage{InputTokens: 9, OutputTokens: 2},
		},
	)
	cancelling := tool.MustNew(tool.Tool[struct{}]{
		Name: "echo", Description: "Cancels the run.", Schema: json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (string, error) {
			cancel()
			return "last words", nil
		},
	})

	agent, err := golem.New[struct{}, string](client, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](cancelling))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(ctx, golem.RunContext[struct{}]{}, "go")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled through the chain", err)
	}
	var runErr *golem.RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("Run() error = %v, cancellation must ride a RunError to keep its evidence", err)
	}
	if runErr.Stage != golem.StageModel {
		t.Fatalf("RunError stage = %s, want model", runErr.Stage)
	}
	if runErr.Partial == nil || len(runErr.Partial.Messages) != 3 {
		t.Fatalf("RunError partial = %+v, want the completed tool round", runErr.Partial)
	}
}

// A tool timeout reports the tool stage with the evidence so far.
func TestToolTimeoutKeepsPartialEvidence(t *testing.T) {
	t.Parallel()

	client := testmodel.New().Respond(
		model.Response{
			Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "call-1", Name: "hang", Args: json.RawMessage(`{}`)},
			}},
		},
	)
	hang := tool.MustNew(tool.Tool[struct{}]{
		Name: "hang", Description: "Blocks until its deadline.", Schema: json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	})

	agent, err := golem.New[struct{}, string](client, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](hang),
		golem.WithToolTimeout[struct{}, string](time.Nanosecond))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want context.DeadlineExceeded through the chain", err)
	}
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageTool {
		t.Fatalf("Run() error = %v, want tool stage for a tool timeout", err)
	}
	if runErr.Partial == nil || runErr.Partial.ToolCalls != 1 {
		t.Fatalf("RunError partial = %+v, want one attempted tool execution", runErr.Partial)
	}
}

// The response that crosses a usage bound is itself evidence: the
// failure carries the conversation including it.
func TestUsageLimitFailureKeepsPartialEvidence(t *testing.T) {
	t.Parallel()

	client := testmodel.New().Respond(
		model.Response{
			Message: model.Message{Role: model.RoleAssistant, Content: "a very long answer"},
			Usage:   model.Usage{InputTokens: 100},
		},
	)
	agent, err := golem.New[struct{}, string](client, golem.DecodeFunc[string](decodeContent),
		golem.WithUsageLimit[struct{}, string](golem.UsageLimit{InputTokens: 50}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageUsage {
		t.Fatalf("Run() error = %v, want usage stage", err)
	}
	partial := runErr.Partial
	if partial == nil {
		t.Fatal("RunError.Partial = nil, want the crossing run's evidence")
	}
	if len(partial.Messages) != 2 || partial.Messages[1].Content != "a very long answer" {
		t.Fatalf("partial messages = %#v, want the crossing response preserved", partial.Messages)
	}
	if partial.Usage.InputTokens != 100 || partial.Requests != 1 {
		t.Fatalf("partial usage/requests = (%d, %d), want (100, 1)", partial.Usage.InputTokens, partial.Requests)
	}
}

// A decode failure keeps the conversation that produced the rejected
// response.
func TestDecodeFailureKeepsPartialEvidence(t *testing.T) {
	t.Parallel()

	client := testmodel.New().Respond(
		model.Response{
			Message: model.Message{Role: model.RoleAssistant, Content: "not a number"},
			Usage:   model.Usage{InputTokens: 6, OutputTokens: 5},
		},
	)
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return "", errors.New("unparseable")
		}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageDecode {
		t.Fatalf("Run() error = %v, want decode stage", err)
	}
	if runErr.Partial == nil || len(runErr.Partial.Messages) != 2 {
		t.Fatalf("RunError partial = %+v, want the rejected response preserved", runErr.Partial)
	}
}

// Correction rounds accumulate: a failure in the second round carries
// the first round's messages and usage plus the second's attempt.
func TestPartialEvidenceAccumulatesAcrossCorrectionRounds(t *testing.T) {
	t.Parallel()

	client := testmodel.New().Respond(
		model.Response{
			Message: model.Message{Role: model.RoleAssistant, Content: "first try"},
			Usage:   model.Usage{InputTokens: 10, OutputTokens: 3},
		},
	).Fail(errMidCorrection)
	decoder := golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
		return "", &model.ModelRetry{Err: errors.New("try again")}
	})
	agent, err := golem.New[struct{}, string](client, decoder,
		golem.WithOutputRetries[struct{}, string](1))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageModel {
		t.Fatalf("Run() error = %v, want model stage", err)
	}
	partial := runErr.Partial
	if partial == nil {
		t.Fatal("RunError.Partial = nil, want accumulated round evidence")
	}
	if partial.Requests != 2 || partial.Usage.InputTokens != 10 {
		t.Fatalf("partial requests/usage = (%d, %d in), want (2, 10)", partial.Requests, partial.Usage.InputTokens)
	}
	if len(partial.Messages) != 3 { // user, rejected response, correction prompt
		t.Fatalf("partial messages = %#v, want user, rejected response, and correction feedback", partial.Messages)
	}
}

// Streamed runs carry partial evidence exactly like unstreamed ones.
func TestStreamFailureKeepsPartialEvidence(t *testing.T) {
	t.Parallel()

	client := testmodel.New().Respond(
		model.Response{
			Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "call-1", Name: "echo", Args: json.RawMessage(`{}`)},
			}},
			Usage: model.Usage{InputTokens: 8, OutputTokens: 2},
		},
	).Fail(errors.New("stream cut"))
	agent, err := golem.New[struct{}, string](client, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](echoToolFor(struct{}{})))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.RunStream(context.Background(), golem.RunContext[struct{}]{}, "go", nil)
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageModel {
		t.Fatalf("RunStream() error = %v, want model stage", err)
	}
	if runErr.Partial == nil || runErr.Partial.Requests != 2 || runErr.Partial.ToolCalls != 1 {
		t.Fatalf("RunError partial = %+v, want the streamed tool round preserved", runErr.Partial)
	}
}

// A turn-limit failure keeps every completed round as evidence.
func TestLoopLimitKeepsPartialEvidence(t *testing.T) {
	t.Parallel()

	client := testmodel.New().Respond(
		model.Response{
			Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "call-1", Name: "echo", Args: json.RawMessage(`{}`)},
			}},
			Usage: model.Usage{InputTokens: 5, OutputTokens: 2},
		},
	)
	agent, err := golem.New[struct{}, string](client, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](echoToolFor(struct{}{})),
		golem.WithMaxIterations[struct{}, string](1))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageLoop {
		t.Fatalf("Run() error = %v, want loop stage", err)
	}
	if runErr.Partial == nil || len(runErr.Partial.Messages) != 3 || runErr.Partial.ToolCalls != 1 {
		t.Fatalf("RunError partial = %+v, want the completed round preserved", runErr.Partial)
	}
}

// Partial messages resume: feeding them back continues the conversation
// with the failed turn's evidence intact.
func TestPartialEvidenceResumesWithHistory(t *testing.T) {
	t.Parallel()

	failing := testmodel.New().Respond(
		model.Response{
			Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "call-1", Name: "echo", Args: json.RawMessage(`{}`)},
			}},
			Usage: model.Usage{InputTokens: 4, OutputTokens: 1},
		},
	).Fail(errors.New("provider gone"))
	agent, err := golem.New[struct{}, string](failing, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](echoToolFor(struct{}{})))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Partial == nil {
		t.Fatalf("Run() error = %v, want partial evidence", err)
	}

	resumed := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "resumed"}},
	)
	resumeAgent, err := golem.New[struct{}, string](resumed, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](echoToolFor(struct{}{})))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := resumeAgent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{},
		runErr.Partial.Messages, "try again")
	if err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}
	if result.Output != "resumed" {
		t.Fatalf("output = %q, want resumed", result.Output)
	}
	// The resumed request carries the failed run's evidence: user,
	// tool round, and the new prompt.
	requests := resumed.Requests()
	if len(requests) != 1 || len(requests[0].Messages) != 4 {
		t.Fatalf("resumed request messages = %#v, want the partial evidence plus the new prompt", requests[0].Messages)
	}
}
