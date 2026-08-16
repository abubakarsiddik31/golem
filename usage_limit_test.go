package golem_test

import (
	"context"
	"errors"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
)

func TestAgentRunSucceedsWithinUsageLimit(t *testing.T) {
	t.Parallel()

	client := &fakeModel{response: model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "42"},
		Usage:   model.Usage{InputTokens: 12, OutputTokens: 3},
	}}
	agent, err := golem.New[struct{}, string](client, echoDecoder(),
		golem.WithUsageLimit[struct{}, string](golem.UsageLimit{TotalTokens: 100}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "answer")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "42" || result.Usage != (model.Usage{InputTokens: 12, OutputTokens: 3}) {
		t.Fatalf("result = %#v", result)
	}
}

func TestAgentRunFailsWhenUsageLimitCrossed(t *testing.T) {
	t.Parallel()

	// Each response reports 6 output tokens; the first decodes fine but
	// already crosses the bound, so the run must fail even though a valid
	// answer existed.
	client := &fakeModel{response: model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "42"},
		Usage:   model.Usage{InputTokens: 4, OutputTokens: 6},
	}}
	agent, err := golem.New[struct{}, string](client, echoDecoder(),
		golem.WithUsageLimit[struct{}, string](golem.UsageLimit{OutputTokens: 5}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "answer")
	if err == nil {
		t.Fatalf("Run() error = nil, want usage limit failure; result = %#v", result)
	}
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageUsage {
		t.Fatalf("error = %v, want the usage stage", err)
	}
	var usageErr *golem.UsageLimitError
	if !errors.As(err, &usageErr) || usageErr.Kind != "output token" || usageErr.Limit != 5 || usageErr.Actual != 6 {
		t.Fatalf("error = %v, want the crossed output bound reported", err)
	}
}

func TestAgentRunCountsUsageAcrossCorrectionRounds(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "no"}, Usage: model.Usage{InputTokens: 3, OutputTokens: 2}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "yes"}, Usage: model.Usage{InputTokens: 3, OutputTokens: 2}},
	}}
	agent, err := golem.New[struct{}, string](client, strictDecoder("yes"),
		golem.WithOutputRetries[struct{}, string](1),
		// The first round fits; the second crosses the cumulative bound.
		golem.WithUsageLimit[struct{}, string](golem.UsageLimit{TotalTokens: 8}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "answer exactly")
	if err == nil {
		t.Fatal("Run() error = nil, want cumulative usage limit failure")
	}
	var usageErr *golem.UsageLimitError
	if !errors.As(err, &usageErr) || usageErr.Kind != "total token" || usageErr.Actual != 10 {
		t.Fatalf("error = %v, want the total bound of 10 reported", err)
	}
	if len(client.requests) != 2 {
		t.Fatalf("model rounds = %d, want 2", len(client.requests))
	}
}

func TestNewRejectsNegativeUsageLimit(t *testing.T) {
	t.Parallel()

	_, err := golem.New[struct{}, string](&fakeModel{}, echoDecoder(),
		golem.WithUsageLimit[struct{}, string](golem.UsageLimit{InputTokens: -1}),
	)
	if err == nil {
		t.Fatal("New() error = nil, want negative usage limit rejection")
	}
}

func TestAgentRunWithoutUsageLimitIsUnbounded(t *testing.T) {
	t.Parallel()

	client := &fakeModel{response: model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "42"},
		Usage:   model.Usage{InputTokens: 10_000, OutputTokens: 9_000},
	}}
	agent, err := golem.New[struct{}, string](client, echoDecoder())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "answer"); err != nil {
		t.Fatalf("Run() error = %v, want success without a configured limit", err)
	}
}
