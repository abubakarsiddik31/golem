package golem

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// agentToolSchema describes the arguments of a tool built by AsTool: one
// required prompt string, the complete task for the sub-agent.
var agentToolSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"prompt": {
			"type": "string",
			"description": "The complete task for the sub-agent. It sees nothing else of the delegating conversation, so include every detail it needs."
		}
	},
	"required": ["prompt"],
	"additionalProperties": false
}`)

// agentToolArgs is the prompt argument of a delegation call.
type agentToolArgs struct {
	Prompt string `json:"prompt"`
}

// AgentToolOption configures a tool built by Agent.AsTool.
type AgentToolOption[Deps any, Output any] func(*agentToolConfig[Deps, Output])

type agentToolConfig[Deps any, Output any] struct {
	result func(context.Context, Output) (string, error)
}

// WithAgentResult replaces how a successful sub-agent output is rendered
// for the delegating model. fn runs inside the tool execution: honor ctx
// and return an error to fail the delegating run at the tool stage. It is
// also the hook for capturing the inner run's typed result or evidence.
func WithAgentResult[Deps any, Output any](fn func(ctx context.Context, output Output) (string, error)) AgentToolOption[Deps, Output] {
	return func(cfg *agentToolConfig[Deps, Output]) {
		cfg.result = fn
	}
}

// AsTool exposes the agent as a tool another agent can request: the model
// passes a prompt, the agent runs it with the delegating run's dependency
// value, and the typed output is rendered to text as the tool result.
//
// Both agents must share the Deps type — the tool carries the delegating
// run's dependency value into the sub-agent's RunContext unchanged. The
// sub-agent sees nothing else of the delegating conversation: the prompt
// argument is its entire input.
//
// A string output is rendered as-is; every other type is JSON-encoded;
// WithAgentResult replaces either. Malformed or empty prompt arguments are
// rejected with *model.ModelRetry, so the delegating run's tool retry
// budget governs correction. Every other sub-agent failure fails the
// delegating run at the tool stage with the inner RunError preserved in
// the chain; cancellation propagates unwrapped. The sub-agent's own
// messages and usage are not part of the delegating run's evidence —
// only the rendered result is.
func (a *Agent[Deps, Output]) AsTool(name, description string, options ...AgentToolOption[Deps, Output]) (tool.Tool[Deps], error) {
	if a == nil {
		return tool.Tool[Deps]{}, fmt.Errorf("golem: agent is required")
	}
	var cfg agentToolConfig[Deps, Output]
	for _, option := range options {
		if option != nil {
			option(&cfg)
		}
	}
	render := cfg.result
	if render == nil {
		render = defaultAgentResult[Output]
	}
	exec := func(ctx context.Context, deps Deps, args json.RawMessage) (string, error) {
		var input agentToolArgs
		if err := json.Unmarshal(args, &input); err != nil {
			return "", &model.ModelRetry{Err: fmt.Errorf("arguments must be an object with a string prompt: %w", err)}
		}
		if input.Prompt == "" {
			return "", &model.ModelRetry{Err: fmt.Errorf("prompt is required")}
		}
		result, err := a.Run(ctx, RunContext[Deps]{Deps: deps}, input.Prompt)
		if err != nil {
			return "", err
		}
		rendered, err := render(ctx, result.Output)
		if err != nil {
			return "", fmt.Errorf("render result: %w", err)
		}
		return rendered, nil
	}
	return tool.New(tool.Tool[Deps]{
		Name:        name,
		Description: description,
		Schema:      agentToolSchema,
		Exec:        exec,
	})
}

// defaultAgentResult renders a sub-agent output for the delegating model:
// strings pass through unquoted, every other type is JSON-encoded.
func defaultAgentResult[Output any](ctx context.Context, output Output) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if text, ok := any(output).(string); ok {
		return text, nil
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return "", fmt.Errorf("encode result: %w", err)
	}
	return string(encoded), nil
}
