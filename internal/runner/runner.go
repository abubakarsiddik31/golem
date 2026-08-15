// Package runner orchestrates the sequential model/tool execution loop. It
// is internal: the public entry point is Agent.Run in the root package,
// which owns stage classification for returned errors.
package runner

import (
	"context"
	"errors"
	"fmt"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// ErrLoopLimit reports that the run exhausted its model turns before the
// model produced a response without tool calls. The core maps it to
// StageLoop.
var ErrLoopLimit = errors.New("runner: model turn limit exceeded")

// ToolError reports a tool execution failure or a model request for a tool
// that was not declared. The core maps it to StageTool and callers can
// reach the cause with errors.Is and errors.As.
type ToolError struct {
	ToolName string
	CallID   string
	Err      error
}

func (e *ToolError) Error() string {
	return fmt.Sprintf("runner: tool %q (call %q): %v", e.ToolName, e.CallID, e.Err)
}

// Unwrap exposes the originating tool error, or nil for an unknown tool.
func (e *ToolError) Unwrap() error {
	return e.Err
}

// Outcome is the terminal state of a completed loop: the final response,
// the full ordered conversation evidence, and usage summed across turns.
type Outcome struct {
	Response model.Response
	Messages []model.Message
	Usage    model.Usage
}

// Execute runs the sequential loop described by ADR 0002: call the model,
// execute any requested tool calls in order, append the evidence, and
// repeat until the model responds without tool calls or a limit or error
// ends the run. The caller's context is checked before every model call
// and tool execution. maxIterations bounds model turns and must be at
// least 1.
func Execute[Deps any](
	ctx context.Context,
	m model.Model,
	tools []tool.Tool[Deps],
	deps Deps,
	req model.Request,
	maxIterations int,
) (Outcome, error) {
	if maxIterations < 1 {
		return Outcome{}, fmt.Errorf("runner: maxIterations must be at least 1, got %d", maxIterations)
	}
	if len(req.ToolSpecs) == 0 && len(tools) > 0 {
		return Outcome{}, fmt.Errorf("runner: tools registered without matching tool specs")
	}

	messages := make([]model.Message, len(req.Messages))
	copy(messages, req.Messages)
	var usage model.Usage

	for turn := 0; ; turn++ {
		if err := ctx.Err(); err != nil {
			return Outcome{}, err
		}
		if turn >= maxIterations {
			return Outcome{}, fmt.Errorf("%w after %d turns", ErrLoopLimit, maxIterations)
		}

		response, err := m.Generate(ctx, model.Request{Messages: messages, ToolSpecs: req.ToolSpecs})
		if err != nil {
			return Outcome{}, err
		}
		messages = append(messages, response.Message)
		usage.InputTokens += response.Usage.InputTokens
		usage.OutputTokens += response.Usage.OutputTokens

		if len(response.Message.ToolCalls) == 0 {
			return Outcome{Response: response, Messages: messages, Usage: usage}, nil
		}

		for _, call := range response.Message.ToolCalls {
			if err := ctx.Err(); err != nil {
				return Outcome{}, err
			}
			declared, ok := findTool(tools, call.Name)
			if !ok {
				return Outcome{}, &ToolError{
					ToolName: call.Name,
					CallID:   call.ID,
					Err:      fmt.Errorf("tool was not declared for this run"),
				}
			}
			result, err := declared.Exec(ctx, deps, call.Args)
			if err != nil {
				return Outcome{}, &ToolError{ToolName: call.Name, CallID: call.ID, Err: err}
			}
			messages = append(messages, model.Message{
				Role:       model.RoleTool,
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Content:    result,
			})
		}
	}
}

func findTool[Deps any](tools []tool.Tool[Deps], name string) (tool.Tool[Deps], bool) {
	for _, t := range tools {
		if t.Name == name {
			return t, true
		}
	}
	return tool.Tool[Deps]{}, false
}
