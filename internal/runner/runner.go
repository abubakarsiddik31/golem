// Package runner orchestrates the sequential model/tool execution loop. It
// is internal: the public entry point is Agent.Run in the root package,
// which owns stage classification for returned errors.
package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// ErrLoopLimit reports that the run exhausted its model turns before the
// model produced a response without tool calls. The core maps it to
// StageLoop.
var ErrLoopLimit = errors.New("runner: model turn limit exceeded")

// ToolError reports a tool execution failure or a model request for a tool
// that was not declared. The core maps it to StageTool and callers can
// reach the cause with errors.Is and errors.As. A tool rejection whose
// correction budget was exhausted lands here too, the *model.ModelRetry
// preserved in the chain.
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

// RetryConfig bounds and paces retries of failed model calls.
// Tool executions and output decoding are never retried; tool rejections
// are fed back to the model under their own budget instead.
type RetryConfig struct {
	// MaxAttempts bounds model calls per turn, including the first. It must
	// be at least 1; 1 disables retries.
	MaxAttempts int
	// Backoff returns the wait after the 1-based number of the attempt that
	// just failed, before the next attempt. nil means no wait.
	Backoff func(attempt int) time.Duration
}

// Execute runs the sequential loop: call the model,
// execute any requested tool calls in order, append the evidence, and
// repeat until the model responds without tool calls or a limit or error
// ends the run. The caller's context is checked before every model call
// and tool execution. maxIterations bounds model turns and must be at
// least 1; retry bounds retried model calls per turn.
// toolRetries bounds how many tool rejections — errors carrying
// *model.ModelRetry — are fed back to the model for correction per run;
// it must not be negative.
func Execute[Deps any](
	ctx context.Context,
	m model.Model,
	tools []tool.Tool[Deps],
	deps Deps,
	req model.Request,
	maxIterations int,
	retry RetryConfig,
	toolRetries int,
) (Outcome, error) {
	if retry.MaxAttempts < 1 {
		return Outcome{}, fmt.Errorf("runner: retry MaxAttempts must be at least 1, got %d", retry.MaxAttempts)
	}
	return execute(ctx, tools, deps, req, maxIterations, toolRetries,
		func(ctx context.Context, request model.Request) (model.Response, error) {
			return generate(ctx, m, request, retry)
		})
}

// ExecuteStream runs the same loop as Execute over streamed model calls:
// every fragment is forwarded to onDelta in arrival order,
// across tool turns and correction feedbacks, and the outcome is the
// assembled run, identical in shape to Execute's. The model must
// implement model.StreamingModel. There is deliberately no retry
// parameter: streamed turns are single-attempt, because retrying a
// stream would replay fragments the caller has already seen; retryable
// failures fail the run classified. A non-nil error from onDelta stops
// the run and is returned as-is.
func ExecuteStream[Deps any](
	ctx context.Context,
	m model.Model,
	tools []tool.Tool[Deps],
	deps Deps,
	req model.Request,
	maxIterations int,
	toolRetries int,
	onDelta func(model.Delta) error,
) (Outcome, error) {
	streamer, ok := m.(model.StreamingModel)
	if !ok {
		return Outcome{}, fmt.Errorf("runner: model %T does not support streaming", m)
	}
	return execute(ctx, tools, deps, req, maxIterations, toolRetries,
		func(ctx context.Context, request model.Request) (model.Response, error) {
			return streamer.GenerateStream(ctx, request, onDelta)
		})
}

// turnCall produces one model response for a turn. Injecting it lets the
// loop run over plain or streamed model calls without duplicating policy.
type turnCall func(ctx context.Context, request model.Request) (model.Response, error)

// execute is the shared loop of Execute and ExecuteStream: identical
// turn limits, tool execution, rejection feedback, and evidence order.
func execute[Deps any](
	ctx context.Context,
	tools []tool.Tool[Deps],
	deps Deps,
	req model.Request,
	maxIterations int,
	toolRetries int,
	call turnCall,
) (Outcome, error) {
	if maxIterations < 1 {
		return Outcome{}, fmt.Errorf("runner: maxIterations must be at least 1, got %d", maxIterations)
	}
	if toolRetries < 0 {
		return Outcome{}, fmt.Errorf("runner: toolRetries must not be negative, got %d", toolRetries)
	}
	if len(req.ToolSpecs) == 0 && len(tools) > 0 {
		return Outcome{}, fmt.Errorf("runner: tools registered without matching tool specs")
	}

	messages := make([]model.Message, len(req.Messages))
	copy(messages, req.Messages)
	var usage model.Usage
	feedbacks := 0

	for turn := 0; ; turn++ {
		if err := ctx.Err(); err != nil {
			return Outcome{}, err
		}
		if turn >= maxIterations {
			return Outcome{}, fmt.Errorf("%w after %d turns", ErrLoopLimit, maxIterations)
		}

		response, err := call(ctx, model.Request{Messages: messages, ToolSpecs: req.ToolSpecs, OutputSchema: req.OutputSchema})
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
				var rejection *model.ModelRetry
				if toolRetries > 0 && errors.As(err, &rejection) {
					// Correction feedback: deliver the rejection
					// as the call's tool result and let the model try again.
					toolRetries--
					feedbacks++
					messages = append(messages, model.Message{
						Role:       model.RoleTool,
						ToolCallID: call.ID,
						ToolName:   call.Name,
						Content: fmt.Sprintf("Your tool call was rejected: %v. "+
							"Correct the arguments and call the tool again.", rejection.Err),
					})
					continue
				}
				return Outcome{}, &ToolError{ToolName: call.Name, CallID: call.ID, Err: terminalToolError(feedbacks, err)}
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

// generate calls the model once per turn, retrying retryable failures up to
// retry.MaxAttempts with context-aware waits between attempts.
// A context error always wins over the model error: cancellation is
// returned raw, never retried or wrapped.
func generate(ctx context.Context, m model.Model, request model.Request, retry RetryConfig) (model.Response, error) {
	for attempt := 1; ; attempt++ {
		response, err := m.Generate(ctx, request)
		if err == nil {
			return response, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return model.Response{}, ctxErr
		}
		if attempt >= retry.MaxAttempts || !model.IsRetryable(err) {
			return model.Response{}, terminalModelError(attempt, err)
		}
		delay := time.Duration(0)
		if retry.Backoff != nil {
			delay = retry.Backoff(attempt)
		}
		if err := waitFor(ctx, delay); err != nil {
			return model.Response{}, err
		}
	}
}

// terminalModelError reports the final model failure. The attempt count is
// attached only when a retry actually happened, so runs without retries
// return the model error unchanged.
func terminalModelError(attempts int, err error) error {
	if attempts > 1 {
		return fmt.Errorf("runner: model failed after %d attempts: %w", attempts, err)
	}
	return err
}

// terminalToolError reports the final tool failure. The attempt count is
// attached only when a correction feedback actually happened, so runs
// without feedback return the tool error unchanged.
func terminalToolError(feedbacks int, err error) error {
	if feedbacks > 0 {
		return fmt.Errorf("tool failed after %d attempts: %w", feedbacks+1, err)
	}
	return err
}

// waitFor pauses for delay and returns nil, or returns the context error
// when ctx is done first. A non-positive delay skips the timer entirely
// but still honors cancellation.
func waitFor(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
