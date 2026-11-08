package golem_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/guard"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

// echoTool counts executions and returns its command argument as text.
type echoTool struct{ executions int }

func (e *echoTool) tool() tool.Tool[struct{}] {
	built, err := tool.New(tool.Tool[struct{}]{
		Name:   "echo",
		Schema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`),
		Exec: func(_ context.Context, _ struct{}, args json.RawMessage) (tool.Result, error) {
			e.executions++
			var input struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return tool.Result{}, err
			}
			return tool.Text("ran: " + input.Command), nil
		},
	})
	if err != nil {
		panic(err)
	}
	return built
}

func TestInputGuardBlockSkipsTheModel(t *testing.T) {
	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: `"never run"`}})
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithInputGuards[struct{}, string](func(_ context.Context, prompt string) guard.Result {
			if strings.Contains(prompt, "weather") {
				return guard.Block("off-topic")
			}
			return guard.Allow()
		}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "what is the weather")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Output != "Your request was blocked: off-topic." {
		t.Fatalf("Output = %q; want the synthesized refusal", result.Output)
	}
	// No model call happened: the refusal is synthesized locally. The
	// original prompt stays in the conversation as evidence.
	if requests := len(client.Requests()); requests != 0 {
		t.Fatalf("model requests = %d; want 0", requests)
	}
	var user, assistant int
	for _, message := range result.Messages {
		switch message.Role {
		case model.RoleUser:
			user++
			if message.Content != "what is the weather" {
				t.Fatalf("user message = %q; want the original prompt", message.Content)
			}
		case model.RoleAssistant:
			assistant++
		}
	}
	if user != 1 || assistant != 1 {
		t.Fatalf("messages user=%d assistant=%d; want 1 each", user, assistant)
	}
}

func TestInputGuardReplaceReachesTheModel(t *testing.T) {
	client := &fakeModel{response: model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: `"ok"`},
	}}
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeJSON[string](),
		golem.WithInputGuards[struct{}, string](func(_ context.Context, prompt string) guard.Result {
			return guard.Replace(strings.ReplaceAll(prompt, "hunter2", "[redacted]"))
		}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "password hunter2"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(client.request.Messages[len(client.request.Messages)-1].Content, "[redacted]") {
		t.Fatalf("model saw prompt %q; want the replacement", client.request.Messages[len(client.request.Messages)-1].Content)
	}
}

func TestOutputGuardBlockFailsAtPolicyStage(t *testing.T) {
	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: `"your SSN is 123"`}})
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeJSON[string](),
		golem.WithOutputGuards[struct{}, string](func(_ context.Context, content string) guard.Result {
			if strings.Contains(content, "SSN") {
				return guard.Block("personal data")
			}
			return guard.Allow()
		}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "who am I")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StagePolicy {
		t.Fatalf("Run error = %v; want RunError at the policy stage", err)
	}
	var blocked *guard.Blocked
	if !errors.As(runErr, &blocked) || blocked.Reason != "personal data" {
		t.Fatalf("RunError chain = %v; want *guard.Blocked", runErr)
	}
	if runErr.Partial == nil || len(runErr.Partial.Messages) == 0 {
		t.Fatal("policy failure lost the partial evidence")
	}
}

func TestOutputGuardReplaceDecodesReplacement(t *testing.T) {
	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: `"raw output"`}})
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithOutputGuards[struct{}, string](func(_ context.Context, content string) guard.Result {
			return guard.Replace("sanitized output")
		}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Output != "sanitized output" {
		t.Fatalf("Output = %q; want the replacement", result.Output)
	}
}

func TestToolGuardBlockSkipsExecution(t *testing.T) {
	echo := &echoTool{}
	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "c1", Name: "echo", Args: json.RawMessage(`{"command":"rm -rf /"}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: `"done"`}})
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeJSON[string](),
		golem.WithTools[struct{}, string](echo.tool()),
		golem.WithToolGuards[struct{}, string](func(_ context.Context, call model.ToolCall) guard.Result {
			return guard.Block("destructive")
		}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "clean up")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if echo.executions != 0 {
		t.Fatalf("tool executions = %d; want 0 — a guard block must skip exec", echo.executions)
	}
	if result.ToolCalls != 0 {
		t.Fatalf("Result.ToolCalls = %d; want 0 for a guard-blocked call", result.ToolCalls)
	}
	for _, message := range result.Messages {
		if message.Role == model.RoleTool && !strings.Contains(message.Content, "blocked by policy") {
			t.Fatalf("tool message = %q; want the guard refusal", message.Content)
		}
	}
}

func TestToolGuardRetryRidesTheBudget(t *testing.T) {
	echo := &echoTool{}
	calls := 0
	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "c1", Name: "echo", Args: json.RawMessage(`{"command":"bad thing"}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "c2", Name: "echo", Args: json.RawMessage(`{"command":"good thing"}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: `"done"`}})
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeJSON[string](),
		golem.WithTools[struct{}, string](echo.tool()),
		golem.WithToolRetries[struct{}, string](3),
		golem.WithToolGuards[struct{}, string](func(_ context.Context, call model.ToolCall) guard.Result {
			calls++
			if strings.Contains(string(call.Args), "bad thing") {
				return guard.Retry("use an allowed command")
			}
			return guard.Allow()
		}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "run something")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if echo.executions != 1 {
		t.Fatalf("tool executions = %d; want 1 (only the corrected call ran)", echo.executions)
	}
	var sawRejection bool
	for _, message := range result.Messages {
		if message.Role == model.RoleTool && strings.Contains(message.Content, "Correct the arguments") {
			sawRejection = true
		}
	}
	if !sawRejection {
		t.Fatal("retry feedback never reached the model")
	}
}

func TestResultGuardBlockAndReplace(t *testing.T) {
	echo := &echoTool{}
	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "c1", Name: "echo", Args: json.RawMessage(`{"command":"secret"}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: `"done"`}})
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeJSON[string](),
		golem.WithTools[struct{}, string](echo.tool()),
		golem.WithResultGuards[struct{}, string](func(_ context.Context, call model.ToolCall, result tool.Result) guard.Result {
			return guard.Block("external content")
		}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "fetch")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if echo.executions != 1 {
		t.Fatalf("tool executions = %d; want 1 — result guards run after exec", echo.executions)
	}
	for _, message := range result.Messages {
		if message.Role == model.RoleTool && strings.Contains(message.Content, "ran: secret") {
			t.Fatal("guarded result reached the model unblocked")
		}
	}
}

func TestResultGuardReplaceThreads(t *testing.T) {
	echo := &echoTool{}
	var seen atomic.Value
	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "c1", Name: "echo", Args: json.RawMessage(`{"command":"SECRET x"}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: `"done"`}})
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeJSON[string](),
		golem.WithTools[struct{}, string](echo.tool()),
		golem.WithResultGuards[struct{}, string](
			func(_ context.Context, _ model.ToolCall, result tool.Result) guard.Result {
				return guard.Replace(strings.ReplaceAll(result.Text, "SECRET", "[redacted]"))
			},
			func(_ context.Context, _ model.ToolCall, result tool.Result) guard.Result {
				seen.Store(result.Text)
				return guard.Allow()
			},
		))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if text, _ := seen.Load().(string); !strings.Contains(text, "[redacted]") {
		t.Fatalf("second guard saw %q; want the replacement", text)
	}
}

func TestToolGuardsUnderParallelCalls(t *testing.T) {
	echo := &echoTool{}
	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "c1", Name: "echo", Args: json.RawMessage(`{"command":"blocked thing"}`)},
			{ID: "c2", Name: "echo", Args: json.RawMessage(`{"command":"allowed thing"}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: `"done"`}})
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeJSON[string](),
		golem.WithTools[struct{}, string](echo.tool()),
		golem.WithParallelToolCalls[struct{}, string](),
		golem.WithToolGuards[struct{}, string](func(_ context.Context, call model.ToolCall) guard.Result {
			if strings.Contains(string(call.Args), "blocked") {
				return guard.Block("denied")
			}
			return guard.Allow()
		}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if echo.executions != 1 {
		t.Fatalf("tool executions = %d; want 1 — the blocked call must not run", echo.executions)
	}
	var allowed, refused int
	for _, message := range result.Messages {
		if message.Role != model.RoleTool {
			continue
		}
		switch {
		case strings.Contains(message.Content, "ran: allowed thing"):
			allowed++
		case strings.Contains(message.Content, "blocked by policy"):
			refused++
		}
	}
	if allowed != 1 || refused != 1 {
		t.Fatalf("tool messages allowed=%d refused=%d; want 1 each", allowed, refused)
	}
}

func TestNilGuardFailsNew(t *testing.T) {
	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: `"ok"`}})
	for name, option := range map[string]golem.Option[struct{}, string]{
		"input":  golem.WithInputGuards[struct{}, string](nil),
		"tool":   golem.WithToolGuards[struct{}, string](nil),
		"result": golem.WithResultGuards[struct{}, string](nil),
		"output": golem.WithOutputGuards[struct{}, string](nil),
	} {
		if _, err := golem.New[struct{}, string](client, golem.DecodeJSON[string](), option); err == nil {
			t.Errorf("%s guard: New accepted a nil guard", name)
		}
	}
}
