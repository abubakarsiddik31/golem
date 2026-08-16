// Package runner orchestrates the sequential model/tool execution loop. It
// is internal: the public entry point is Agent.Run in the root package,
// which owns stage classification for returned errors.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
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

// ToolConfig controls execution of declared tools. Zero values preserve the
// original sequential behavior: no default timeout and no parallel calls.
type ToolConfig struct {
	DefaultRetries int
	DefaultTimeout time.Duration
	Parallel       bool
}

// Execute runs the sequential loop: call the model,
// execute any requested tool calls in order, append the evidence, and
// repeat until the model responds without tool calls or a limit or error
// ends the run. The caller's context is checked before every model call
// and tool execution. maxIterations bounds model turns and must be
// at least 1; retry bounds retried model calls per turn.
// toolRetries bounds how many tool rejections — errors carrying
// *model.ModelRetry — are fed back to the model for correction per run;
// it must not be negative. outputTool names a synthesized tool whose call
// ends the run as the final output; empty means no output tool.
func Execute[Deps any](
	ctx context.Context,
	m model.Model,
	tools []tool.Tool[Deps],
	deps Deps,
	req model.Request,
	maxIterations int,
	retry RetryConfig,
	toolRetries int,
	outputTool string,
) (Outcome, error) {
	return ExecuteWithToolConfig(ctx, m, tools, deps, req, maxIterations, retry,
		ToolConfig{DefaultRetries: toolRetries}, outputTool)
}

// ExecuteWithToolConfig runs the loop with explicit tool policy.
func ExecuteWithToolConfig[Deps any](
	ctx context.Context,
	m model.Model,
	tools []tool.Tool[Deps],
	deps Deps,
	req model.Request,
	maxIterations int,
	retry RetryConfig,
	toolConfig ToolConfig,
	outputTool string,
) (Outcome, error) {
	if retry.MaxAttempts < 1 {
		return Outcome{}, fmt.Errorf("runner: retry MaxAttempts must be at least 1, got %d", retry.MaxAttempts)
	}
	return execute(ctx, tools, deps, req, maxIterations, toolConfig, outputTool,
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
// the run and is returned as-is. outputTool names a synthesized tool
// whose call ends the run as the final output; empty means no output tool.
func ExecuteStream[Deps any](
	ctx context.Context,
	m model.Model,
	tools []tool.Tool[Deps],
	deps Deps,
	req model.Request,
	maxIterations int,
	toolRetries int,
	outputTool string,
	onDelta func(model.Delta) error,
) (Outcome, error) {
	return ExecuteStreamWithToolConfig(ctx, m, tools, deps, req, maxIterations,
		ToolConfig{DefaultRetries: toolRetries}, outputTool, onDelta)
}

// ExecuteStreamWithToolConfig runs streamed turns with explicit tool policy.
func ExecuteStreamWithToolConfig[Deps any](
	ctx context.Context,
	m model.Model,
	tools []tool.Tool[Deps],
	deps Deps,
	req model.Request,
	maxIterations int,
	toolConfig ToolConfig,
	outputTool string,
	onDelta func(model.Delta) error,
) (Outcome, error) {
	streamer, ok := m.(model.StreamingModel)
	if !ok {
		return Outcome{}, fmt.Errorf("runner: model %T does not support streaming", m)
	}
	return execute(ctx, tools, deps, req, maxIterations, toolConfig, outputTool,
		func(ctx context.Context, request model.Request) (model.Response, error) {
			return streamer.GenerateStream(ctx, request, onDelta)
		})
}

// turnCall produces one model response for a turn. Injecting it lets the
// loop run over plain or streamed model calls without duplicating policy.
type turnCall func(ctx context.Context, request model.Request) (model.Response, error)

// execute is the shared loop of Execute and ExecuteStream: identical
// turn limits, tool execution, rejection feedback, and evidence order.
// outputTool, when non-empty, names the synthesized output tool: the
// model's first call to it ends the run.
func execute[Deps any](
	ctx context.Context,
	tools []tool.Tool[Deps],
	deps Deps,
	req model.Request,
	maxIterations int,
	toolConfig ToolConfig,
	outputTool string,
	call turnCall,
) (Outcome, error) {
	if maxIterations < 1 {
		return Outcome{}, fmt.Errorf("runner: maxIterations must be at least 1, got %d", maxIterations)
	}
	if toolConfig.DefaultRetries < 0 {
		return Outcome{}, fmt.Errorf("runner: default tool retries must not be negative, got %d", toolConfig.DefaultRetries)
	}
	if toolConfig.DefaultTimeout < 0 {
		return Outcome{}, fmt.Errorf("runner: default tool timeout must not be negative, got %s", toolConfig.DefaultTimeout)
	}
	if len(req.ToolSpecs) == 0 && len(tools) > 0 {
		return Outcome{}, fmt.Errorf("runner: tools registered without matching tool specs")
	}
	if outputTool != "" && findSpec(req.ToolSpecs, outputTool) == nil {
		return Outcome{}, fmt.Errorf("runner: output tool %q has no matching tool spec", outputTool)
	}

	messages := make([]model.Message, len(req.Messages))
	copy(messages, req.Messages)
	var usage model.Usage
	feedbacks := make(map[string]int)

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

		if outputTool != "" {
			if call, ok := findCallByName(response.Message.ToolCalls, outputTool); ok {
				return finishOnOutput(messages, usage, response, call), nil
			}
		}

		toolMessages, err := runToolCalls(ctx, tools, deps, response.Message.ToolCalls, toolConfig, feedbacks)
		if err != nil {
			return Outcome{}, err
		}
		messages = append(messages, toolMessages...)
	}
}

type toolCallResult[Deps any] struct {
	call     model.ToolCall
	declared tool.Tool[Deps]
	result   string
	err      error
}

// runToolCalls validates every requested call, executes them either in
// emission order or concurrent barrier-separated groups, then emits results
// in emission order regardless of completion order.
func runToolCalls[Deps any](ctx context.Context, tools []tool.Tool[Deps], deps Deps, calls []model.ToolCall, config ToolConfig, feedbacks map[string]int) ([]model.Message, error) {
	results := make([]toolCallResult[Deps], len(calls))
	for i, call := range calls {
		declared, ok := findTool(tools, call.Name)
		if !ok {
			return nil, &ToolError{ToolName: call.Name, CallID: call.ID, Err: fmt.Errorf("tool was not declared for this run")}
		}
		results[i] = toolCallResult[Deps]{call: call, declared: declared}
	}
	for start := 0; start < len(results); {
		end := start + 1
		if config.Parallel && !results[start].declared.Sequential {
			for end < len(results) && !results[end].declared.Sequential {
				end++
			}
		}
		runToolGroup(ctx, results[start:end], deps, config.DefaultTimeout, config.Parallel && end-start > 1)
		start = end
	}
	messages := make([]model.Message, 0, len(results))
	for _, item := range results {
		if item.err == nil {
			messages = append(messages, model.Message{Role: model.RoleTool, ToolCallID: item.call.ID, ToolName: item.call.Name, Content: item.result})
			continue
		}
		if errors.Is(item.err, context.Canceled) || errors.Is(item.err, context.DeadlineExceeded) {
			return nil, item.err
		}
		var rejection *model.ModelRetry
		limit := config.DefaultRetries
		if item.declared.MaxRetries != nil {
			limit = *item.declared.MaxRetries
		}
		if feedbacks[item.declared.Name] < limit && errors.As(item.err, &rejection) {
			feedbacks[item.declared.Name]++
			messages = append(messages, model.Message{Role: model.RoleTool, ToolCallID: item.call.ID, ToolName: item.call.Name,
				Content: fmt.Sprintf("Your tool call was rejected: %v. Correct the arguments and call the tool again.", rejection.Err)})
			continue
		}
		return nil, &ToolError{ToolName: item.call.Name, CallID: item.call.ID, Err: terminalToolError(feedbacks[item.declared.Name], item.err)}
	}
	return messages, nil
}

func runToolGroup[Deps any](ctx context.Context, group []toolCallResult[Deps], deps Deps, timeout time.Duration, parallel bool) {
	if !parallel {
		for i := range group {
			if err := ctx.Err(); err != nil {
				group[i].err = err
				return
			}
			group[i].result, group[i].err = executeTool(ctx, group[i].declared, deps, group[i].call.Args, timeout)
		}
		return
	}
	var wg sync.WaitGroup
	for i := range group {
		wg.Add(1)
		go func(item *toolCallResult[Deps]) {
			defer wg.Done()
			item.result, item.err = executeTool(ctx, item.declared, deps, item.call.Args, timeout)
		}(&group[i])
	}
	wg.Wait()
}

func findTool[Deps any](tools []tool.Tool[Deps], name string) (tool.Tool[Deps], bool) {
	for _, t := range tools {
		if t.Name == name {
			return t, true
		}
	}
	return tool.Tool[Deps]{}, false
}

func findSpec(specs []model.ToolSpec, name string) *model.ToolSpec {
	for i := range specs {
		if specs[i].Name == name {
			return &specs[i]
		}
	}
	return nil
}

func findCallByName(calls []model.ToolCall, name string) (model.ToolCall, bool) {
	for _, call := range calls {
		if call.Name == name {
			return call, true
		}
	}
	return model.ToolCall{}, false
}

// executeTool applies the narrowest configured deadline to one call. The
// tool owns any work it starts and must honor ctx cancellation; this wrapper
// does not leave a goroutine behind to race a non-cooperative tool.
func executeTool[Deps any](ctx context.Context, declared tool.Tool[Deps], deps Deps, args json.RawMessage, defaultTimeout time.Duration) (string, error) {
	timeout := defaultTimeout
	if declared.Timeout != 0 {
		timeout = declared.Timeout
	}
	if timeout <= 0 {
		return declared.Exec(ctx, deps, args)
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := declared.Exec(callCtx, deps, args)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if callCtx.Err() != nil {
		return "", callCtx.Err()
	}
	return result, err
}

// finishOnOutput ends the run on the model's call to the output tool.
// The call's arguments become the final response content — the decoder's
// validation boundary — while every other call in the same response is
// closed with an interrupted result: co-emitted calls are not executed,
// and the call/result pairing providers require stays intact. The output
// call itself is left for the caller to close, because only it knows
// whether the decoder accepts the arguments (a recorded result) or
// rejects them for correction (a rejection bound to the call).
func finishOnOutput(messages []model.Message, usage model.Usage, response model.Response, call model.ToolCall) Outcome {
	for _, other := range response.Message.ToolCalls {
		if other.ID == call.ID {
			continue
		}
		messages = append(messages, model.Message{
			Role:       model.RoleTool,
			ToolCallID: other.ID,
			ToolName:   other.Name,
			Content:    interruptedToolResult,
		})
	}
	final := response
	final.Message.Content = string(call.Args)
	return Outcome{Response: final, Messages: messages, Usage: usage}
}

// interruptedToolResult is the neutral result synthesized for a tool call
// that produced no outcome. It states the absence of a result — not a
// tool failure — so the model neither assumes an outcome nor treats the
// call as a provider error.
const interruptedToolResult = "Tool call was interrupted before execution; no result was produced. Call the tool again if you need its result."

// RepairHistory restores the call/result pairing providers require: every
// tool call is answered by a tool result, and every tool result references a
// call in the history. A run that ended before a requested tool executed — a
// crash, a cancelled stream, or hand-built history — leaves calls without
// results; each receives a synthesized result, placed directly after the
// assistant message that requested it, in call order. Results whose call is
// absent, including results that precede their call, are dropped: no
// provider accepts them.
//
// Repair only adds or removes pairing evidence; it never rewrites messages.
// It is deterministic and idempotent: synthesized results carry no
// wall-clock data, so repairing an already-repaired history returns it
// unchanged, and repeated resumes stay prompt-cache friendly. Pairing is by
// call ID; calls without an ID cannot be paired and are left as-is.
func RepairHistory(history []model.Message) []model.Message {
	pending := make(map[string]struct{}, len(history))
	repaired := make([]model.Message, 0, len(history))
	dropped := false
	for _, message := range history {
		switch message.Role {
		case model.RoleAssistant:
			repaired = append(repaired, message)
			for _, call := range message.ToolCalls {
				if call.ID != "" {
					pending[call.ID] = struct{}{}
				}
			}
		case model.RoleTool:
			if _, requested := pending[message.ToolCallID]; requested {
				delete(pending, message.ToolCallID)
				repaired = append(repaired, message)
			} else {
				dropped = true
			}
		default:
			repaired = append(repaired, message)
		}
	}
	if len(pending) == 0 && !dropped {
		return history
	}
	if len(pending) == 0 {
		return repaired
	}
	withResults := make([]model.Message, 0, len(repaired)+len(pending))
	for _, message := range repaired {
		withResults = append(withResults, message)
		for _, call := range message.ToolCalls {
			if _, unanswered := pending[call.ID]; unanswered {
				withResults = append(withResults, model.Message{
					Role:       model.RoleTool,
					ToolCallID: call.ID,
					ToolName:   call.Name,
					Content:    interruptedToolResult,
				})
			}
		}
	}
	return withResults
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
