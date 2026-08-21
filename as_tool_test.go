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
)

type delegationDeps struct{ Year int }

func decodeContent(_ context.Context, r model.Response) (string, error) {
	return r.Message.Content, nil
}

func agentCall(id, args string) model.Response {
	return model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
		{ID: id, Name: "researcher", Args: json.RawMessage(args)},
	}}}
}

func TestAsToolDelegatesPromptAndSharesDeps(t *testing.T) {
	t.Parallel()

	var seenYear int
	innerModel := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Paris"}},
	)
	inner, err := golem.New[delegationDeps, string](
		innerModel,
		golem.DecodeFunc[string](decodeContent),
		golem.WithInstructionsFunc[delegationDeps, string](func(_ context.Context, runCtx golem.RunContext[delegationDeps]) string {
			seenYear = runCtx.Deps.Year
			return "Answer in one word."
		}),
	)
	if err != nil {
		t.Fatalf("inner New() error = %v", err)
	}
	agentTool, err := inner.AsTool("researcher", "Answers geography questions")
	if err != nil {
		t.Fatalf("AsTool() error = %v", err)
	}

	outerModel := testmodel.New().Respond(
		agentCall("c1", `{"prompt":"Capital of France?"}`),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "The capital is Paris."}},
	)
	outer, err := golem.New[delegationDeps, string](
		outerModel,
		golem.DecodeFunc[string](decodeContent),
		golem.WithTools[delegationDeps, string](agentTool),
	)
	if err != nil {
		t.Fatalf("outer New() error = %v", err)
	}

	result, err := outer.Run(context.Background(), golem.RunContext[delegationDeps]{Deps: delegationDeps{Year: 1789}}, "I need a capital.")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "The capital is Paris." {
		t.Fatalf("Output = %q", result.Output)
	}
	if seenYear != 1789 {
		t.Fatalf("sub-agent deps year = %d, want 1789", seenYear)
	}

	// The sub-agent received exactly the prompt argument, under its own
	// resolved instructions.
	innerRequests := innerModel.Requests()
	if len(innerRequests) != 1 {
		t.Fatalf("sub-agent model turns = %d, want 1", len(innerRequests))
	}
	innerMessages := innerRequests[0].Messages
	if len(innerMessages) != 2 ||
		innerMessages[0].Role != model.RoleSystem || innerMessages[0].Content != "Answer in one word." ||
		innerMessages[1].Role != model.RoleUser || innerMessages[1].Content != "Capital of France?" {
		t.Fatalf("sub-agent messages = %#v", innerMessages)
	}

	// The delegating model saw one inspectable tool spec.
	outerRequests := outerModel.Requests()
	if len(outerRequests) != 2 {
		t.Fatalf("outer model turns = %d, want 2", len(outerRequests))
	}
	specs := outerRequests[0].ToolSpecs
	if len(specs) != 1 || specs[0].Name != "researcher" || len(specs[0].Schema) == 0 || !json.Valid(specs[0].Schema) {
		t.Fatalf("tool specs = %#v", specs)
	}

	// The rendered result is the tool result evidence: a string output
	// passes through unquoted.
	if len(result.Messages) != 4 {
		t.Fatalf("evidence messages = %d, want 4", len(result.Messages))
	}
	toolResult := result.Messages[2]
	if toolResult.Role != model.RoleTool || toolResult.ToolName != "researcher" || toolResult.ToolCallID != "c1" {
		t.Fatalf("tool result message = %#v", toolResult)
	}
	if toolResult.Content != "Paris" {
		t.Fatalf("tool result content = %q, want the raw string output", toolResult.Content)
	}
}

func TestAsToolJSONEncodesStructResult(t *testing.T) {
	t.Parallel()

	type report struct {
		Summary string
		Count   int
	}
	innerModel := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "ignored by the decoder"}},
	)
	inner, err := golem.New[struct{}, report](
		innerModel,
		golem.DecodeFunc[report](func(_ context.Context, r model.Response) (report, error) {
			return report{Summary: "found 3", Count: 3}, nil
		}),
	)
	if err != nil {
		t.Fatalf("inner New() error = %v", err)
	}
	agentTool, err := inner.AsTool("researcher", "Answers questions")
	if err != nil {
		t.Fatalf("AsTool() error = %v", err)
	}

	outerModel := testmodel.New().Respond(
		agentCall("c1", `{"prompt":"count the things"}`),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	)
	outer, err := golem.New[struct{}, string](
		outerModel,
		golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](agentTool),
	)
	if err != nil {
		t.Fatalf("outer New() error = %v", err)
	}
	result, err := outer.Run(context.Background(), golem.RunContext[struct{}]{}, "count")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := result.Messages[2].Content; got != `{"Summary":"found 3","Count":3}` {
		t.Fatalf("tool result content = %q, want JSON-encoded report", got)
	}
}

func TestAsToolWithAgentResultOverridesRendering(t *testing.T) {
	t.Parallel()

	innerModel := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Paris"}},
	)
	inner, err := golem.New[struct{}, string](
		innerModel,
		golem.DecodeFunc[string](decodeContent),
	)
	if err != nil {
		t.Fatalf("inner New() error = %v", err)
	}
	agentTool, err := inner.AsTool("researcher", "Answers questions",
		golem.WithAgentResult[struct{}, string](func(_ context.Context, output string) (string, error) {
			return "RENDERED: " + output, nil
		}),
	)
	if err != nil {
		t.Fatalf("AsTool() error = %v", err)
	}

	outerModel := testmodel.New().Respond(
		agentCall("c1", `{"prompt":"Capital of France?"}`),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	)
	outer, err := golem.New[struct{}, string](
		outerModel,
		golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](agentTool),
	)
	if err != nil {
		t.Fatalf("outer New() error = %v", err)
	}
	result, err := outer.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := result.Messages[2].Content; got != "RENDERED: Paris" {
		t.Fatalf("tool result content = %q", got)
	}
}

func TestAsToolInnerFailureReachesToolStage(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("provider exploded")
	innerModel := testmodel.New().Fail(sentinel)
	inner, err := golem.New[struct{}, string](
		innerModel,
		golem.DecodeFunc[string](decodeContent),
	)
	if err != nil {
		t.Fatalf("inner New() error = %v", err)
	}
	agentTool, err := inner.AsTool("researcher", "Answers questions")
	if err != nil {
		t.Fatalf("AsTool() error = %v", err)
	}

	outerModel := testmodel.New().Respond(agentCall("c1", `{"prompt":"anything"}`))
	outer, err := golem.New[struct{}, string](
		outerModel,
		golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](agentTool),
	)
	if err != nil {
		t.Fatalf("outer New() error = %v", err)
	}

	_, err = outer.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	if err == nil {
		t.Fatal("Run() error = nil, want the sub-agent failure to fail the run")
	}
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageTool {
		t.Fatalf("Run() error = %v, want a RunError at the tool stage", err)
	}
	// RunError -> ToolError -> the sub-agent's own RunError, stage model.
	innerStage := errors.Unwrap(errors.Unwrap(runErr))
	var innerErr *golem.RunError
	if !errors.As(innerStage, &innerErr) || innerErr.Stage != golem.StageModel {
		t.Fatalf("inner error = %#v, want the sub-agent RunError at the model stage", innerStage)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("errors.Is(err, sentinel) = false, cause not preserved: %v", err)
	}
}

func TestAsToolCancellationPropagatesUnwrapped(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	innerModel := testmodel.Func(func(ctx context.Context, request model.Request) (model.Response, error) {
		cancel()
		<-ctx.Done()
		return model.Response{}, ctx.Err()
	})
	inner, err := golem.New[struct{}, string](
		innerModel,
		golem.DecodeFunc[string](decodeContent),
	)
	if err != nil {
		t.Fatalf("inner New() error = %v", err)
	}
	agentTool, err := inner.AsTool("researcher", "Answers questions")
	if err != nil {
		t.Fatalf("AsTool() error = %v", err)
	}

	outerModel := testmodel.New().Respond(agentCall("c1", `{"prompt":"anything"}`))
	outer, err := golem.New[struct{}, string](
		outerModel,
		golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](agentTool),
	)
	if err != nil {
		t.Fatalf("outer New() error = %v", err)
	}

	_, err = outer.Run(ctx, golem.RunContext[struct{}]{}, "go")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	var runErr *golem.RunError
	if errors.As(err, &runErr) {
		t.Fatalf("cancellation must surface unwrapped, got RunError{Stage: %s}", runErr.Stage)
	}
}

func TestAsToolRejectsBadPromptsAsCorrectable(t *testing.T) {
	t.Parallel()

	innerModel := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "inner done"}},
	)
	inner, err := golem.New[struct{}, string](
		innerModel,
		golem.DecodeFunc[string](decodeContent),
	)
	if err != nil {
		t.Fatalf("inner New() error = %v", err)
	}
	agentTool, err := inner.AsTool("researcher", "Answers questions")
	if err != nil {
		t.Fatalf("AsTool() error = %v", err)
	}

	outerModel := testmodel.New().Respond(
		agentCall("c1", `{"prompt":""}`), // missing prompt
		agentCall("c2", `{"prompt":42}`), // wrong argument type
		agentCall("c3", `{"prompt":"real task"}`),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "finished"}},
	)
	outer, err := golem.New[struct{}, string](
		outerModel,
		golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](agentTool),
		golem.WithToolRetries[struct{}, string](2),
	)
	if err != nil {
		t.Fatalf("outer New() error = %v", err)
	}

	result, err := outer.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "finished" {
		t.Fatalf("Output = %q", result.Output)
	}

	// The sub-agent ran once, on the corrected prompt only. It has no
	// instructions, so its request is the single user message.
	innerRequests := innerModel.Requests()
	if len(innerRequests) != 1 ||
		len(innerRequests[0].Messages) != 1 ||
		innerRequests[0].Messages[0].Role != model.RoleUser ||
		innerRequests[0].Messages[0].Content != "real task" {
		t.Fatalf("sub-agent requests = %#v, want one run on the corrected prompt", innerRequests)
	}

	// Each bad call was fed back as a rejection the model could correct.
	outerRequests := outerModel.Requests()
	if len(outerRequests) != 4 {
		t.Fatalf("outer model turns = %d, want 4", len(outerRequests))
	}
	for i, want := range map[int]string{1: "prompt is required", 2: "string prompt"} {
		results := outerRequests[i].Messages
		rejection := results[len(results)-1]
		if rejection.Role != model.RoleTool || !strings.Contains(rejection.Content, want) {
			t.Fatalf("request %d rejection = %#v, want it to mention %q", i+1, rejection, want)
		}
	}
}

func TestAsToolValidation(t *testing.T) {
	t.Parallel()

	innerModel := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "ok"}},
	)
	inner, err := golem.New[struct{}, string](
		innerModel,
		golem.DecodeFunc[string](decodeContent),
	)
	if err != nil {
		t.Fatalf("inner New() error = %v", err)
	}
	if _, err := inner.AsTool("", "description"); err == nil {
		t.Fatal("AsTool with an empty name = nil error, want rejection")
	}
	var nilAgent *golem.Agent[struct{}, string]
	if _, err := nilAgent.AsTool("name", "description"); err == nil {
		t.Fatal("AsTool on a nil agent = nil error, want rejection")
	}
}
