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

type weather struct {
	City string `json:"city"`
}

var weatherSchema = json.RawMessage(`{
	"type": "object",
	"properties": {"city": {"type": "string"}},
	"required": ["city"],
	"additionalProperties": false
}`)

func outputCall(id, city string) model.Response {
	return model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
		{ID: id, Name: "record_weather", Args: json.RawMessage(`{"city":"` + city + `"}`)},
	}}}
}

func TestOutputToolEndsRunOnFirstCall(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{outputCall("out-1", "Lagos")}}
	agent, err := golem.New[struct{}, weather](client, golem.DecodeJSON[weather](),
		golem.WithOutputTool[struct{}, weather]("record_weather",
			"Record the final weather report.", weatherSchema))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "Weather for Lagos.")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// The model offered the synthesized output tool and called it.
	specs := client.requests[0].ToolSpecs
	if len(specs) != 1 || specs[0].Name != "record_weather" ||
		specs[0].Description != "Record the final weather report." ||
		string(specs[0].Schema) != string(weatherSchema) {
		t.Fatalf("output tool spec = %#v", specs)
	}

	// The call's arguments are the final output, decoded like any other
	// response content.
	if result.Output.City != "Lagos" {
		t.Fatalf("Output = %#v", result.Output)
	}

	// Evidence keeps the pairing providers require: the output call is
	// closed with a recorded result after decoding.
	if len(result.Messages) != 3 {
		t.Fatalf("evidence = %#v", result.Messages)
	}
	closing := result.Messages[2]
	if closing.Role != model.RoleTool || closing.ToolCallID != "out-1" || closing.ToolName != "record_weather" {
		t.Fatalf("closing result = %#v", closing)
	}
	if closing.Content != "Result recorded." {
		t.Fatalf("closing content = %q", closing.Content)
	}
}

func TestOutputToolCoEmittedCallsAreNotExecuted(t *testing.T) {
	t.Parallel()

	executions := 0
	roll := tool.MustNew(tool.Tool[struct{}]{
		Name:        "roll",
		Description: "Roll a die.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (string, error) {
			executions++
			return "6", nil
		},
	})
	client := &queuedModel{responses: []model.Response{{Message: model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{
			{ID: "roll-1", Name: "roll", Args: json.RawMessage(`{}`)},
			{ID: "out-1", Name: "record_weather", Args: json.RawMessage(`{"city":"Lagos"}`)},
		},
	}}}}
	agent, err := golem.New[struct{}, weather](client, golem.DecodeJSON[weather](),
		golem.WithTools[struct{}, weather](roll),
		golem.WithOutputTool[struct{}, weather]("record_weather", "", weatherSchema))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "Weather for Lagos.")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// The first output call ends the run: the co-emitted roll is not
	// executed and is closed with an interrupted result so the
	// conversation stays provider-valid.
	if executions != 0 {
		t.Fatalf("roll executions = %d, want 0", executions)
	}
	if len(result.Messages) != 4 {
		t.Fatalf("evidence = %#v", result.Messages)
	}
	interrupted := result.Messages[2]
	if interrupted.Role != model.RoleTool || interrupted.ToolCallID != "roll-1" {
		t.Fatalf("interrupted result = %#v", interrupted)
	}
	if !strings.Contains(interrupted.Content, "interrupted before execution") {
		t.Fatalf("interrupted content = %q", interrupted.Content)
	}
	if result.Messages[3].ToolCallID != "out-1" || result.Messages[3].Content != "Result recorded." {
		t.Fatalf("closing result = %#v", result.Messages[3])
	}
}

func TestOutputToolRejectionBindsToCall(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		outputCall("out-1", "Paris"),
		outputCall("out-2", "Lagos"),
	}}
	decodes := 0
	decoder := golem.DecodeFunc[weather](func(ctx context.Context, response model.Response) (weather, error) {
		decodes++
		var w weather
		if err := json.Unmarshal([]byte(response.Message.Content), &w); err != nil {
			return weather{}, err
		}
		if w.City == "Paris" {
			return weather{}, &model.ModelRetry{Err: errors.New("city must be in Africa")}
		}
		return w, nil
	})
	agent, err := golem.New[struct{}, weather](client, decoder,
		golem.WithOutputTool[struct{}, weather]("record_weather", "", weatherSchema),
		golem.WithOutputRetries[struct{}, weather](1))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "Weather for Lagos.")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if decodes != 2 || result.Output.City != "Lagos" {
		t.Fatalf("decodes = %d, output = %#v", decodes, result.Output)
	}

	// The correction round tells the model about the rejection through the
	// output call's own tool result, so the next request stays paired.
	rejection := client.requests[1].Messages[2]
	if rejection.Role != model.RoleTool || rejection.ToolCallID != "out-1" || rejection.ToolName != "record_weather" {
		t.Fatalf("correction feedback = %#v", rejection)
	}
	if !strings.Contains(rejection.Content, "rejected") || !strings.Contains(rejection.Content, "city must be in Africa") {
		t.Fatalf("correction content = %q", rejection.Content)
	}
}

func TestNewValidatesOutputToolConfiguration(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		options []golem.Option[struct{}, weather]
	}{
		{
			name: "schema and tool together",
			options: []golem.Option[struct{}, weather]{
				golem.WithOutputSchema[struct{}, weather](weatherSchema),
				golem.WithOutputTool[struct{}, weather]("record_weather", "", weatherSchema),
			},
		},
		{
			name: "invalid schema",
			options: []golem.Option[struct{}, weather]{
				golem.WithOutputTool[struct{}, weather]("record_weather", "", json.RawMessage(`{`)),
			},
		},
		{
			name: "empty schema",
			options: []golem.Option[struct{}, weather]{
				golem.WithOutputTool[struct{}, weather]("record_weather", "", nil),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := &queuedModel{}
			if _, err := golem.New[struct{}, weather](client, golem.DecodeJSON[weather](), tc.options...); err == nil {
				t.Fatalf("New() error = nil, want configuration rejection")
			}
		})
	}

	// The output tool name must not shadow an application tool.
	roll := tool.MustNew(tool.Tool[struct{}]{
		Name:   "roll",
		Schema: json.RawMessage(`{"type":"object"}`),
		Exec:   func(ctx context.Context, deps struct{}, args json.RawMessage) (string, error) { return "6", nil },
	})
	if _, err := golem.New[struct{}, weather](&queuedModel{}, golem.DecodeJSON[weather](),
		golem.WithTools[struct{}, weather](roll),
		golem.WithOutputTool[struct{}, weather]("roll", "", weatherSchema)); err == nil {
		t.Fatal("New() error = nil, want name collision rejection")
	}
}

func TestRunStreamOutputTool(t *testing.T) {
	t.Parallel()

	client := streamClient(outputCall("out-1", "Lagos"))
	agent, err := golem.New[struct{}, weather](client, golem.DecodeJSON[weather](),
		golem.WithOutputTool[struct{}, weather]("record_weather", "", weatherSchema))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var deltas []model.Delta
	result, err := agent.RunStream(context.Background(), golem.RunContext[struct{}]{}, "Weather for Lagos.",
		func(d model.Delta) error {
			deltas = append(deltas, d)
			return nil
		})
	if err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}
	if result.Output.City != "Lagos" {
		t.Fatalf("Output = %#v", result.Output)
	}
	if len(deltas) != 1 || deltas[0].ToolCalls[0].ID != "out-1" {
		t.Fatalf("deltas = %#v", deltas)
	}
	if result.Messages[len(result.Messages)-1].Content != "Result recorded." {
		t.Fatalf("closing evidence = %#v", result.Messages[len(result.Messages)-1])
	}
}
