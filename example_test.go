package golem_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// diceModel scripts the tool exchange: it requests the player-name tool
// once, then produces a final answer. Real applications implement
// model.Model with a provider adapter.
type diceModel struct{ requests []model.Request }

func (m *diceModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	m.requests = append(m.requests, request)
	for _, message := range request.Messages {
		if message.Role == model.RoleTool {
			return model.Response{
				Message: model.Message{Role: model.RoleAssistant, Content: fmt.Sprintf("winner: %s", message.Content)},
				Usage:   model.Usage{InputTokens: 54, OutputTokens: 2},
			}, nil
		}
	}
	return model.Response{
		Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "get_player_name", Args: json.RawMessage(`{}`)},
		}},
		Usage: model.Usage{InputTokens: 54, OutputTokens: 2},
	}, nil
}

// conversationModel answers with the last user prompt it has seen.
type conversationModel struct{}

func (m *conversationModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	last := ""
	for _, message := range request.Messages {
		if message.Role == model.RoleUser {
			last = message.Content
		}
	}
	return model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: fmt.Sprintf("heard: %s", last)},
	}, nil
}

// ExampleAgent_RunWithHistory continues a conversation across two runs:
// the first result's messages become the second run's history, and the
// second result carries the full chained conversation.
func ExampleAgent_RunWithHistory() {
	agent, err := golem.New[struct{}, string](&conversationModel{},
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}))
	if err != nil {
		log.Fatal(err)
	}
	runCtx := golem.RunContext[struct{}]{}

	first, err := agent.Run(context.Background(), runCtx, "hello")
	if err != nil {
		log.Fatal(err)
	}
	second, err := agent.RunWithHistory(context.Background(), runCtx, first.Messages, "goodbye")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(first.Output)
	fmt.Println(second.Output)
	fmt.Println(len(second.Messages), "messages in the chained conversation")
	// Output:
	// heard: hello
	// heard: goodbye
	// 4 messages in the chained conversation
}

// pickyModel answers with a word first, then with the digit once corrected.
type pickyModel struct{ calls int }

func (m *pickyModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	m.calls++
	content := "seven"
	if m.calls > 1 {
		content = "7"
	}
	return model.Response{Message: model.Message{Role: model.RoleAssistant, Content: content}}, nil
}

// ExampleWithOutputRetries shows a decoder rejecting a correctable
// response: the run feeds the rejection back to the model, which answers
// again within the configured budget.
func ExampleWithOutputRetries() {
	agent, err := golem.New[struct{}, int](&pickyModel{},
		golem.DecodeFunc[int](func(ctx context.Context, response model.Response) (int, error) {
			value, err := strconv.Atoi(strings.TrimSpace(response.Message.Content))
			if err != nil {
				return 0, &model.ModelRetry{Err: fmt.Errorf("answer must be an integer, got %q", response.Message.Content)}
			}
			return value, nil
		}),
		golem.WithOutputRetries[struct{}, int](2),
	)
	if err != nil {
		log.Fatal(err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "pick a number")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result.Output)
	fmt.Println(len(result.Messages), "messages in the corrected conversation")
	// Output:
	// 7
	// 4 messages in the corrected conversation
}

// learningModel requests the roll tool with an invalid argument first,
// then corrects the call once it sees the rejection come back.
type learningModel struct{ calls int }

func (m *learningModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	last := request.Messages[len(request.Messages)-1]
	if last.Role == model.RoleTool && !strings.Contains(last.Content, "rejected") {
		return model.Response{
			Message: model.Message{Role: model.RoleAssistant, Content: fmt.Sprintf("the die %s", last.Content)},
		}, nil
	}
	m.calls++
	n := 0
	if m.calls > 1 {
		n = 4
	}
	return model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
		{ID: fmt.Sprintf("call-%d", m.calls), Name: "roll", Args: json.RawMessage(fmt.Sprintf(`{"n":%d}`, n))},
	}}}, nil
}

// ExampleWithToolRetries shows a tool rejecting correctable arguments:
// the run delivers the rejection as the call's tool result, and the model
// calls again with fixed arguments within the configured budget.
func ExampleWithToolRetries() {
	roll := tool.MustNew(tool.Tool[struct{}]{
		Name:        "roll",
		Description: "Roll a die; n must be positive.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer"}}}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (string, error) {
			var input struct {
				N int `json:"n"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return "", err
			}
			if input.N <= 0 {
				return "", &model.ModelRetry{Err: fmt.Errorf("n must be positive, got %d", input.N)}
			}
			return fmt.Sprintf("rolled %d", input.N), nil
		},
	})

	agent, err := golem.New[struct{}, string](&learningModel{},
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](roll),
		golem.WithToolRetries[struct{}, string](2),
	)
	if err != nil {
		log.Fatal(err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "roll a 4")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result.Output)
	fmt.Println(len(result.Messages), "messages in the corrected run")
	// Output:
	// the die rolled 4
	// 6 messages in the corrected run
}

// morningModel streams its answer as two fragments.
type morningModel struct{}

func (m *morningModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	return model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "good morning"}}, nil
}

func (m *morningModel) GenerateStream(ctx context.Context, request model.Request, onDelta func(model.Delta) error) (model.Response, error) {
	for _, fragment := range []string{"good ", "morning"} {
		if err := onDelta(model.Delta{Content: fragment}); err != nil {
			return model.Response{}, err
		}
	}
	return model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "good morning"}}, nil
}

// ExampleAgent_RunStream shows a run that streams every fragment to the
// callback while producing the same typed result as Run.
func ExampleAgent_RunStream() {
	agent, err := golem.New[struct{}, string](&morningModel{},
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}))
	if err != nil {
		log.Fatal(err)
	}

	var fragments []string
	result, err := agent.RunStream(context.Background(), golem.RunContext[struct{}]{}, "greet me",
		func(d model.Delta) error {
			fragments = append(fragments, d.Content)
			return nil
		})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(strings.Join(fragments, "|"))
	fmt.Println(result.Output)
	// Output:
	// good |morning
	// good morning
}

// forecastModel answers with JSON shaped by the output schema.
type forecastModel struct{}

func (m *forecastModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	return model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: `{"city":"Lagos","celsius":31}`},
		Usage:   model.Usage{InputTokens: 20, OutputTokens: 6},
	}, nil
}

// ExampleWithOutputSchema pairs a declared output schema — sent to the
// model as structured-output instructions by adapters that support them —
// with the JSON decoder that validates the response content.
func ExampleWithOutputSchema() {
	type weather struct {
		City    string `json:"city"`
		Celsius int    `json:"celsius"`
	}
	agent, err := golem.New[struct{}, weather](&forecastModel{}, golem.DecodeJSON[weather](),
		golem.WithOutputSchema[struct{}, weather](json.RawMessage(`{
			"type": "object",
			"properties": {"city": {"type": "string"}, "celsius": {"type": "integer"}},
			"required": ["city", "celsius"],
			"additionalProperties": false
		}`)),
	)
	if err != nil {
		log.Fatal(err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "forecast for Lagos")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%s: %d°C\n", result.Output.City, result.Output.Celsius)
	// Output:
	// Lagos: 31°C
}

// reportingModel calls the output tool with its final arguments.
type reportingModel struct{}

func (m *reportingModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	return model.Response{
		Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "out-1", Name: "record_weather", Args: json.RawMessage(`{"city":"Lagos","celsius":31}`)},
		}},
		Usage: model.Usage{InputTokens: 20, OutputTokens: 6},
	}, nil
}

// ExampleWithOutputTool declares tool-mode structured output: the schema
// becomes the parameters of a synthesized output tool, the run ends on the
// model's first call to it, and the call's arguments reach the decoder as
// the final response content.
func ExampleWithOutputTool() {
	type weather struct {
		City    string `json:"city"`
		Celsius int    `json:"celsius"`
	}
	agent, err := golem.New[struct{}, weather](&reportingModel{}, golem.DecodeJSON[weather](),
		golem.WithOutputTool[struct{}, weather]("record_weather",
			"Record the final weather report.", json.RawMessage(`{
				"type": "object",
				"properties": {"city": {"type": "string"}, "celsius": {"type": "integer"}},
				"required": ["city", "celsius"],
				"additionalProperties": false
			}`)),
	)
	if err != nil {
		log.Fatal(err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "forecast for Lagos")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%s: %d°C\n", result.Output.City, result.Output.Celsius)
	// Output:
	// Lagos: 31°C
}

// instructedModel echoes the instructions it was given, if any.
type instructedModel struct{}

func (m *instructedModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	for _, message := range request.Messages {
		if message.Role == model.RoleSystem {
			return model.Response{
				Message: model.Message{Role: model.RoleAssistant, Content: message.Content},
			}, nil
		}
	}
	return model.Response{Message: model.Message{Role: model.RoleAssistant, Content: ""}}, nil
}

// ExampleWithInstructionsFunc shows instructions resolved per run: the
// function's result joins the static instructions, and both flow to the
// model as the run's system guidance.
func ExampleWithInstructionsFunc() {
	type player struct{ Name string }
	agent, err := golem.New[player, string](&instructedModel{},
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithInstructions[player, string]("Always greet the player."),
		golem.WithInstructionsFunc[player, string](
			func(ctx context.Context, runCtx golem.RunContext[player]) string {
				return "The player's name is " + runCtx.Deps.Name + "."
			}),
	)
	if err != nil {
		log.Fatal(err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[player]{Deps: player{Name: "Anne"}}, "greet")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result.Output)
	// Output:
	// Always greet the player.
	//
	// The player's name is Anne.
}

// verboseModel reports heavy usage on every response.
type verboseModel struct{}

func (m *verboseModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	return model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "an expensive answer"},
		Usage:   model.Usage{InputTokens: 1200, OutputTokens: 800},
	}, nil
}

// ExampleWithUsageLimit shows a run stopped at the usage stage: the
// response that crosses the bound fails the run, with the crossed
// dimension inspectable through the typed cause.
func ExampleWithUsageLimit() {
	agent, err := golem.New[struct{}, string](&verboseModel{},
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithUsageLimit[struct{}, string](golem.UsageLimit{TotalTokens: 1000}),
	)
	if err != nil {
		log.Fatal(err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "answer")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) {
		log.Fatal(err)
	}
	fmt.Println(runErr.Stage)
	fmt.Println(runErr.Err)
	// Output:
	// usage
	// run exceeded the total token limit of 1000 (used 2000)
}

// ExampleAgent demonstrates an agent that executes a typed tool with an
// explicit dependency value and returns the full run evidence.
func ExampleAgent() {
	getPlayerName := tool.MustNew(tool.Tool[string]{
		Name:        "get_player_name",
		Description: "Get the player's name.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, playerName string, args json.RawMessage) (string, error) {
			return playerName, nil
		},
	})

	agent, err := golem.New[string, string](
		&diceModel{},
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[string, string](getPlayerName),
	)
	if err != nil {
		log.Fatal(err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[string]{Deps: "Anne"}, "My guess is 4")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result.Output)
	fmt.Println(len(result.Messages), "messages,", result.Usage.OutputTokens, "output tokens")
	fmt.Println("requests:", result.Requests, "- tool calls:", result.ToolCalls)
	// Output:
	// winner: Anne
	// 4 messages, 4 output tokens
	// requests: 2 - tool calls: 1
}

// delegatingModel hands the question to the researcher tool, then answers
// from its result. It stands in for the planner's provider.
type delegatingModel struct{}

func (m *delegatingModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	for _, message := range request.Messages {
		if message.Role == model.RoleTool {
			return model.Response{
				Message: model.Message{Role: model.RoleAssistant,
					Content: fmt.Sprintf("the researcher says: %s", message.Content)},
			}, nil
		}
	}
	return model.Response{
		Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "researcher", Args: json.RawMessage(`{"prompt":"capital of France?"}`)},
		}},
	}, nil
}

// factModel stands in for the specialist's provider: one run, one fact.
type factModel struct{}

func (m *factModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	return model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Paris"}}, nil
}

func ExampleAgent_AsTool() {
	specialist, err := golem.New[struct{}, string](&factModel{},
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}))
	if err != nil {
		log.Fatal(err)
	}
	research, err := specialist.AsTool("researcher", "Answers one geography question.")
	if err != nil {
		log.Fatal(err)
	}
	planner, err := golem.New[struct{}, string](&delegatingModel{},
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](research))
	if err != nil {
		log.Fatal(err)
	}

	result, err := planner.Run(context.Background(), golem.RunContext[struct{}]{}, "I need the capital of France.")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result.Output)
	// Output:
	// the researcher says: Paris
}
