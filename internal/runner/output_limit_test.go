package runner_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/internal/runner"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

func outputLimitTool(t *testing.T, text string, maxOutput int) tool.Tool[deps] {
	t.Helper()
	return tool.MustNew(tool.Tool[deps]{
		Name:           "big",
		Description:    "big",
		Schema:         json.RawMessage(`{"type":"object"}`),
		MaxOutputBytes: maxOutput,
		Exec: func(context.Context, deps, json.RawMessage) (tool.Result, error) {
			return tool.Text(text), nil
		},
	})
}

func runOutputLimit(t *testing.T, text string, maxOutput, limit int) model.Message {
	t.Helper()
	big := outputLimitTool(t, text, maxOutput)
	client := &scriptedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "big", Args: json.RawMessage(`{}`)},
		}}},
		textResponse("done", model.Usage{}),
	}}
	spec := model.ToolSpec{Name: big.Name, Description: big.Description, Schema: big.Schema}
	request := model.Request{
		Messages:  []model.Message{{Role: model.RoleUser, Content: "hi"}},
		ToolSpecs: []model.ToolSpec{spec},
	}
	config := runner.ToolConfig{OutputLimit: limit}
	outcome, err := runner.ExecuteWithToolConfig(context.Background(), client, []tool.Tool[deps]{big},
		deps{}, request, 3, noRetries, config, "", nil, nil, runner.RunIdentity{})
	if err != nil {
		t.Fatalf("ExecuteWithToolConfig() error = %v", err)
	}
	for _, message := range outcome.Messages {
		if message.Role == model.RoleTool {
			return message
		}
	}
	t.Fatal("no tool message in outcome")
	return model.Message{}
}

func TestOutputLimitTruncatesToolResult(t *testing.T) {
	t.Parallel()

	message := runOutputLimit(t, strings.Repeat("a", 500)+"END", 0, 80)
	if len(message.Content) > 80 {
		t.Fatalf("tool content = %d bytes", len(message.Content))
	}
	if !strings.Contains(message.Content, "original size") || !strings.HasSuffix(message.Content, "END") {
		t.Fatalf("reduced content = %q", message.Content)
	}
}

func TestOutputLimitPerToolOverride(t *testing.T) {
	t.Parallel()

	message := runOutputLimit(t, strings.Repeat("b", 500), 40, 200)
	if len(message.Content) > 40 {
		t.Fatalf("per-tool override ignored: %d bytes", len(message.Content))
	}
}

func TestOutputLimitTinyBudgetDropsMarker(t *testing.T) {
	t.Parallel()

	message := runOutputLimit(t, strings.Repeat("é", 100), 0, 10)
	if len(message.Content) > 10 {
		t.Fatalf("tiny budget exceeded: %d bytes", len(message.Content))
	}
	if strings.Contains(message.Content, "original size") {
		t.Fatalf("marker did not fit but was kept: %q", message.Content)
	}
}
