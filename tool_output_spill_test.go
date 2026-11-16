package golem_test

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

func spillTool(t *testing.T, text string) tool.Tool[struct{}] {
	t.Helper()
	return tool.MustNew(tool.Tool[struct{}]{
		Name:        "big",
		Description: "big",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(context.Context, struct{}, json.RawMessage) (tool.Result, error) {
			return tool.Text(text), nil
		},
	})
}

func runBigTool(t *testing.T, agent *golem.Agent[struct{}, string], client *queuedModel, responses ...model.Response) {
	t.Helper()
	client.responses = append(client.responses, responses...)
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestSpillStoresFullResultAndReadToolRestoresIt(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("payload ", 700)
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "big", Args: json.RawMessage(`{}`)},
		}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	}}
	store := tool.NewMemorySpill()
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](spillTool(t, text)),
		golem.WithToolOutputSpill[struct{}, string](store, 100),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	runBigTool(t, agent, client)

	marker := toolMessage(t, client.requests).Content
	if !strings.Contains(marker, "tool output spilled") || len(marker) > 600 {
		t.Fatalf("spill marker = %q", marker)
	}
	match := regexp.MustCompile(`handle "([^"]+)"`).FindStringSubmatch(marker)
	if len(match) != 2 {
		t.Fatalf("marker has no handle: %q", marker)
	}
	handle := match[1]
	if !requestAdvertises(t, client.requests[0], golem.SpillReadToolName) {
		t.Fatalf("read tool was not advertised: %#v", client.requests[0].ToolSpecs)
	}

	runBigTool(t, agent, client,
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			ID:   "call-2",
			Name: golem.SpillReadToolName,
			Args: json.RawMessage(`{"handle":"` + handle + `"}`),
		}}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	)
	if got := lastToolMessage(t, client.requests[len(client.requests)-1].Messages); got != text {
		t.Fatalf("read tool returned %d bytes, want the full %d-byte payload", len(got), len(text))
	}
}

func TestSummaryCondensesAndFallsBack(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("detail ", 200)
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "big", Args: json.RawMessage(`{}`)},
		}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	}}
	var summaryTool string
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](spillTool(t, text)),
		golem.WithToolOutputSummary[struct{}, string](func(_ context.Context, toolName, _ string) (string, error) {
			summaryTool = toolName
			return "condensed", nil
		}, 100),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	runBigTool(t, agent, client)
	if summaryTool != "big" || toolMessage(t, client.requests).Content != "condensed" {
		t.Fatalf("summary tool=%q content=%q", summaryTool, toolMessage(t, client.requests).Content)
	}

	client = &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "big", Args: json.RawMessage(`{}`)},
		}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	}}
	agent, err = golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](spillTool(t, text)),
		golem.WithToolOutputSummary[struct{}, string](func(context.Context, string, string) (string, error) {
			return "", errors.New("summarizer offline")
		}, 100),
		golem.WithToolOutputLimit[struct{}, string](80),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	runBigTool(t, agent, client)
	if got := toolMessage(t, client.requests).Content; len(got) > 80 || !strings.Contains(got, "tool output truncated") {
		t.Fatalf("summary fallback = %q", got)
	}
}

func TestSpillFallsBackToTruncation(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("x", 500)
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "big", Args: json.RawMessage(`{}`)},
		}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](spillTool(t, text)),
		golem.WithToolOutputSpill[struct{}, string](failingSpill{}, 100),
		golem.WithToolOutputLimit[struct{}, string](80),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	runBigTool(t, agent, client)
	got := toolMessage(t, client.requests).Content
	if len(got) > 80 || !strings.Contains(got, "tool output truncated") || strings.Contains(got, "spilled") {
		t.Fatalf("spill fallback = %q", got)
	}
}

func TestSpillConfigurationValidation(t *testing.T) {
	t.Parallel()

	if _, err := golem.New[struct{}, string](&fakeModel{}, decoderOf(),
		golem.WithToolOutputSpill[struct{}, string](nil, 10)); err == nil {
		t.Fatal("nil spill store accepted")
	}
	if _, err := golem.New[struct{}, string](&fakeModel{}, decoderOf(),
		golem.WithToolOutputSpill[struct{}, string](tool.NewMemorySpill(), 0)); err == nil {
		t.Fatal("zero spill threshold accepted")
	}
	if _, err := golem.New[struct{}, string](&fakeModel{}, decoderOf(),
		golem.WithToolOutputSummary[struct{}, string](nil, 10)); err == nil {
		t.Fatal("nil summarizer accepted")
	}
	if _, err := golem.New[struct{}, string](&fakeModel{}, decoderOf(),
		golem.WithToolOutputSpill[struct{}, string](tool.NewMemorySpill(), 10),
		golem.WithToolOutputSummary[struct{}, string](func(context.Context, string, string) (string, error) {
			return "x", nil
		}, 10)); err == nil {
		t.Fatal("spill and summary together accepted")
	}
	if _, err := golem.New[struct{}, string](&fakeModel{}, decoderOf(),
		golem.WithTools[struct{}, string](spillReadCollision(t)),
		golem.WithToolOutputSpill[struct{}, string](tool.NewMemorySpill(), 10)); err == nil {
		t.Fatal("reserved read-tool name collision accepted")
	}
}

func TestReadToolRejectsMissingHandle(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Name: golem.SpillReadToolName,
			Args: json.RawMessage(`{"handle":"missing"}`),
		}}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithToolOutputSpill[struct{}, string](tool.NewMemorySpill(), 10),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	message := toolMessage(t, client.requests)
	if !message.Failed || !strings.Contains(message.Content, "no spilled result") {
		t.Fatalf("missing handle result = %#v", message)
	}
}

func requestAdvertises(t *testing.T, request model.Request, name string) bool {
	t.Helper()
	for _, spec := range request.ToolSpecs {
		if spec.Name == name {
			return true
		}
	}
	return false
}

func spillReadCollision(t *testing.T) tool.Tool[struct{}] {
	t.Helper()
	return tool.MustNew(tool.Tool[struct{}]{
		Name:        golem.SpillReadToolName,
		Description: "collision",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(context.Context, struct{}, json.RawMessage) (tool.Result, error) {
			return tool.Text(""), nil
		},
	})
}

type failingSpill struct{}

func (failingSpill) Put(context.Context, string) (string, error) {
	return "", errors.New("spill offline")
}
func (failingSpill) Read(context.Context, string, int, int) (string, int, error) {
	return "", 0, tool.ErrSpillNotFound
}
