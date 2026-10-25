package golem_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// deferrableTool builds a minimal declared tool for catalog tests.
func deferrableTool(name string) tool.Tool[struct{}] {
	return tool.MustNew(tool.Tool[struct{}]{
		Name:        name,
		Description: "Tool named " + name + ".",
		Schema:      json.RawMessage(`{"type":"object","properties":{"location":{"type":"string"}}}`),
		Exec: func(context.Context, struct{}, json.RawMessage) (tool.Result, error) {
			return tool.Text(name + " ran"), nil
		},
	})
}

func toolSpecNames(specs []model.ToolSpec) []string {
	names := make([]string, len(specs))
	for i, spec := range specs {
		names[i] = spec.Name
	}
	return names
}

func equalNames(got []string, want ...string) bool {
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

func TestWithDeferredToolsAdvertisesSearchToolThenReveals(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "s1", Name: "search_tools", Args: json.RawMessage(`{"query":"weather"}`)},
		}}},
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "w1", Name: "weather", Args: json.RawMessage(`{"location":"Paris"}`)},
		}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "sunny"}},
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](deferrableTool("echo")),
		golem.WithDeferredTools[struct{}, string](deferrableTool("weather")))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "weather?")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "sunny" {
		t.Fatalf("Output = %q, want sunny", result.Output)
	}
	if result.ToolCalls != 2 {
		t.Fatalf("ToolCalls = %d, want 2 (search + weather)", result.ToolCalls)
	}
	if len(client.requests) != 3 {
		t.Fatalf("model requests = %d, want 3", len(client.requests))
	}
	if got := toolSpecNames(client.requests[0].ToolSpecs); !equalNames(got, "echo", "search_tools") {
		t.Fatalf("first request specs = %v, want [echo search_tools]", got)
	}
	if got := toolSpecNames(client.requests[1].ToolSpecs); !equalNames(got, "echo", "search_tools", "weather") {
		t.Fatalf("second request specs = %v, want [echo search_tools weather]", got)
	}
}

func TestWithDeferredToolsKeepsRevealAcrossCorrectionRounds(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "s1", Name: "search_tools", Args: json.RawMessage(`{"query":"weather"}`)},
		}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "almost"}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "exact"}},
	}}
	agent, err := golem.New[struct{}, string](client, strictDecoder("exact"),
		golem.WithTools[struct{}, string](deferrableTool("echo")),
		golem.WithDeferredTools[struct{}, string](deferrableTool("weather")),
		golem.WithOutputRetries[struct{}, string](1))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "weather?"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(client.requests) != 3 {
		t.Fatalf("model requests = %d, want 3", len(client.requests))
	}
	// The correction round replays history, so the earlier search still
	// reveals the tool without a second search.
	if got := toolSpecNames(client.requests[2].ToolSpecs); !equalNames(got, "echo", "search_tools", "weather") {
		t.Fatalf("correction request specs = %v, want [echo search_tools weather]", got)
	}
}

func TestWithDeferredToolsRejectsCallBeforeReveal(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "w1", Name: "weather", Args: json.RawMessage(`{"location":"Paris"}`)},
		}}},
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithDeferredTools[struct{}, string](deferrableTool("weather")))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "weather?")
	if err == nil {
		t.Fatal("Run() error = nil, want a deferred-tool rejection")
	}
	if !strings.Contains(err.Error(), "deferred") || !strings.Contains(err.Error(), "search_tools") {
		t.Fatalf("error = %q, want it to name the deferred tool and search_tools", err)
	}
}

func TestNewRejectsToolChoiceWithDeferredTools(t *testing.T) {
	t.Parallel()

	_, err := golem.New[struct{}, string](&queuedModel{}, decoderOf(),
		golem.WithTools[struct{}, string](deferrableTool("echo")),
		golem.WithDeferredTools[struct{}, string](deferrableTool("weather")),
		golem.WithToolChoice[struct{}, string]("echo"))
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("New() error = %v, want mutual-exclusion rejection", err)
	}
}

func TestNewRejectsReservedSearchToolName(t *testing.T) {
	t.Parallel()

	if _, err := golem.New[struct{}, string](&queuedModel{}, decoderOf(),
		golem.WithDeferredTools[struct{}, string](deferrableTool("search_tools"))); err == nil {
		t.Fatal("New() error = nil, want reserved-name rejection for a deferred tool")
	}
	if _, err := golem.New[struct{}, string](&queuedModel{}, decoderOf(),
		golem.WithTools[struct{}, string](deferrableTool("search_tools")),
		golem.WithDeferredTools[struct{}, string](deferrableTool("weather"))); err == nil {
		t.Fatal("New() error = nil, want reserved-name rejection for an eager tool")
	}
}

func TestNewRejectsReservedSearchToolNameForOutputTool(t *testing.T) {
	t.Parallel()

	_, err := golem.New[struct{}, string](&queuedModel{}, decoderOf(),
		golem.WithDeferredTools[struct{}, string](deferrableTool("weather")),
		golem.WithOutputTool[struct{}, string]("search_tools", "Answer", json.RawMessage(`{"type":"object"}`)))
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("New() error = %v, want reserved-name rejection for the output tool", err)
	}
}

func TestNewRejectsDuplicateNameAcrossEagerAndDeferred(t *testing.T) {
	t.Parallel()

	_, err := golem.New[struct{}, string](&queuedModel{}, decoderOf(),
		golem.WithTools[struct{}, string](deferrableTool("weather")),
		golem.WithDeferredTools[struct{}, string](deferrableTool("weather")))
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("New() error = %v, want duplicate-name rejection", err)
	}
}

func TestNewRejectsNegativeToolSearchLimit(t *testing.T) {
	t.Parallel()

	_, err := golem.New[struct{}, string](&queuedModel{}, decoderOf(),
		golem.WithDeferredTools[struct{}, string](deferrableTool("weather")),
		golem.WithToolSearchLimit[struct{}, string](-1))
	if err == nil || !strings.Contains(err.Error(), "tool search limit") {
		t.Fatalf("New() error = %v, want negative-limit rejection", err)
	}
}

func TestWithToolSearchLimitCapsRevealedTools(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "s1", Name: "search_tools", Args: json.RawMessage(`{"query":"data"}`)},
		}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithDeferredTools[struct{}, string](
			deferrableTool("data_alpha"), deferrableTool("data_beta"), deferrableTool("data_gamma")),
		golem.WithToolSearchLimit[struct{}, string](2))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "data?"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// Limit 2 of three equally ranked matches, alphabetical: alpha, beta.
	if got := toolSpecNames(client.requests[1].ToolSpecs); !equalNames(got, "search_tools", "data_alpha", "data_beta") {
		t.Fatalf("revealed specs = %v, want [search_tools data_alpha data_beta]", got)
	}
}
