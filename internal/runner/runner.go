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

// Outcome is the terminal state of a loop: the final response, the full
// ordered conversation evidence, usage summed across turns, the last
// turn's terminal cause, and activity counts for usage bounds. An error
// return keeps the evidence the loop accumulated: Messages runs through
// the last completed model turn — a failed provider call contributes
// nothing, and a tool batch that fails contributes none of its results —
// Usage sums the completed turns, FinishReason is the last completed
// turn's cause, and the counts include the failed provider attempt.
// Callers surface that partial evidence; a run that failed before any
// activity returns the zero Outcome.
type Outcome struct {
	Response model.Response
	Messages []model.Message
	Usage    model.Usage
	// FinishReason is the provider's terminal cause of the last
	// completed model turn: the final response's cause on success, the
	// last completed turn's cause on failure.
	FinishReason model.FinishReason
	// ModelCalls counts provider calls, including retried attempts.
	ModelCalls int
	// ToolExecutions counts tool executions attempted; unknown-tool
	// requests and interrupted output-tool co-emissions do not count.
	// Calls that deferred do not count either; their approved re-runs on
	// resume do.
	ToolExecutions int
	// Pending is non-empty when the loop paused on deferred tool calls:
	// the run ended mid-conversation awaiting their resolution. Response
	// is the assistant turn that requested the calls, and Messages ends
	// with the results of the batch's executed calls only.
	Pending []PendingCall
}

// PendingCall is one tool call that deferred instead of executing.
type PendingCall struct {
	Call model.ToolCall
	// Defer is the sentinel the tool returned, with its kind and reason.
	Defer tool.Deferred
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

// EventKind identifies one observable point of the execution loop.
type EventKind string

const (
	// EventModelStart precedes one provider call attempt.
	EventModelStart EventKind = "model_start"
	// EventModelEnd follows one provider call attempt, carrying the
	// attempt's usage and error.
	EventModelEnd EventKind = "model_end"
	// EventToolStart precedes one tool execution.
	EventToolStart EventKind = "tool_start"
	// EventToolEnd follows one tool execution, carrying the result or the
	// error; a correction rejection carries a *model.ModelRetry error.
	EventToolEnd EventKind = "tool_end"
	// EventOutputRejected marks a decoder rejection starting a correction
	// round. The agent emits it; the runner loop never does, because only
	// the agent knows the decoder's verdict.
	EventOutputRejected EventKind = "output_rejected"
	// EventDeferred marks a tool call that deferred instead of executing:
	// the run pauses with the call pending, surfaced in Outcome.Pending.
	// No EventToolEnd follows for a deferred call.
	EventDeferred EventKind = "deferred"
)

// Event is one observation of an executing run. The fields a kind
// carries are documented on the kind; the rest are zero. Kind, order,
// and per-kind field meaning are a compatibility promise for observers.
type Event struct {
	// Kind identifies which fields carry meaning.
	Kind EventKind
	// Turn indexes the model turn within the current loop. Correction
	// rounds restart the loop; EventOutputRejected marks the boundary.
	Turn int
	// Attempt is the 1-based provider call attempt within the turn.
	Attempt int
	// CallID and ToolName identify one requested tool execution.
	CallID   string
	ToolName string
	// Args is the raw model-produced argument object of the call.
	Args json.RawMessage
	// Result is the tool result text of a successful execution.
	Result string
	// Err is the attempt or execution error. A correction rejection
	// carries *model.ModelRetry.
	Err error
	// Usage is the provider-recorded consumption of one attempt.
	Usage model.Usage
}

// Observer receives run events synchronously, in deterministic
// execution order: model attempts in call order, tool starts before and
// tool ends after execution, with parallel groups emitting starts in
// model emission order before the group runs and ends in the same order
// after it completes. A call whose execution returns a deferred sentinel
// reports EventDeferred in place of its tool end, in emission order
// within its group. Observers run inline with execution and must not
// block; cancel the run context to stop the run from an observer.
type Observer func(Event)

// emitEvent forwards event to emit when one is registered.
func emitEvent(emit Observer, event Event) {
	if emit != nil {
		emit(event)
	}
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
		ToolConfig{DefaultRetries: toolRetries}, outputTool, nil)
}

// ExecuteWithToolConfig runs the loop with explicit tool policy and an
// optional event observer; see Observer for the ordering guarantees.
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
	emit Observer,
) (Outcome, error) {
	if retry.MaxAttempts < 1 {
		return Outcome{}, fmt.Errorf("runner: retry MaxAttempts must be at least 1, got %d", retry.MaxAttempts)
	}
	return execute(ctx, tools, deps, req, maxIterations, toolConfig, outputTool, emit,
		func(ctx context.Context, request model.Request, turn int) (model.Response, int, error) {
			return generate(ctx, m, request, retry, turn, emit)
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
		ToolConfig{DefaultRetries: toolRetries}, outputTool, nil, onDelta)
}

// ExecuteStreamWithToolConfig runs streamed turns with explicit tool
// policy and an optional event observer; see Observer for the ordering
// guarantees.
func ExecuteStreamWithToolConfig[Deps any](
	ctx context.Context,
	m model.Model,
	tools []tool.Tool[Deps],
	deps Deps,
	req model.Request,
	maxIterations int,
	toolConfig ToolConfig,
	outputTool string,
	emit Observer,
	onDelta func(model.Delta) error,
) (Outcome, error) {
	streamer, ok := m.(model.StreamingModel)
	if !ok {
		return Outcome{}, fmt.Errorf("runner: model %T does not support streaming", m)
	}
	return execute(ctx, tools, deps, req, maxIterations, toolConfig, outputTool, emit,
		func(ctx context.Context, request model.Request, turn int) (model.Response, int, error) {
			emitEvent(emit, Event{Kind: EventModelStart, Turn: turn, Attempt: 1})
			response, err := streamer.GenerateStream(ctx, request, onDelta)
			event := Event{Kind: EventModelEnd, Turn: turn, Attempt: 1, Usage: response.Usage}
			if err != nil {
				event.Err = err
			}
			emitEvent(emit, event)
			return response, 1, err
		})
}

// turnCall produces one model response for a turn and reports how many
// provider calls the turn consumed, retries included. Injecting it lets
// the loop run over plain or streamed model calls without duplicating
// policy. turn is the 0-based index of the turn in this loop, for event
// attribution.
type turnCall func(ctx context.Context, request model.Request, turn int) (model.Response, int, error)

// runCounts accumulates activity across the turns of one loop.
type runCounts struct {
	modelCalls     int
	toolExecutions int
}

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
	emit Observer,
	call turnCall,
) (Outcome, error) {
	var counts runCounts
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
	var lastFinish model.FinishReason
	feedbacks := make(map[string]int)

	for turn := 0; ; turn++ {
		if err := ctx.Err(); err != nil {
			return partialOutcome(messages, usage, counts, lastFinish), err
		}
		if turn >= maxIterations {
			return partialOutcome(messages, usage, counts, lastFinish), fmt.Errorf("%w after %d turns", ErrLoopLimit, maxIterations)
		}

		response, providerCalls, err := call(ctx, model.Request{Messages: messages, ToolSpecs: req.ToolSpecs, OutputSchema: req.OutputSchema}, turn)
		if err != nil {
			counts.modelCalls += providerCalls
			return partialOutcome(messages, usage, counts, lastFinish), err
		}
		counts.modelCalls += providerCalls
		lastFinish = response.FinishReason
		messages = append(messages, response.Message)
		usage.InputTokens += response.Usage.InputTokens
		usage.OutputTokens += response.Usage.OutputTokens

		if len(response.Message.ToolCalls) == 0 {
			return Outcome{Response: response, Messages: messages, Usage: usage,
				FinishReason: response.FinishReason,
				ModelCalls:   counts.modelCalls, ToolExecutions: counts.toolExecutions}, nil
		}

		if outputTool != "" {
			if call, ok := findCallByName(response.Message.ToolCalls, outputTool); ok {
				outcome := finishOnOutput(messages, usage, response, call)
				outcome.ModelCalls = counts.modelCalls
				outcome.ToolExecutions = counts.toolExecutions
				outcome.FinishReason = response.FinishReason
				return outcome, nil
			}
		}

		toolMessages, pending, err := runToolCalls(ctx, tools, deps, response.Message.ToolCalls, toolConfig, feedbacks, &counts, turn, emit)
		if err != nil {
			return partialOutcome(messages, usage, counts, lastFinish), err
		}
		messages = append(messages, toolMessages...)
		if len(pending) > 0 {
			return Outcome{Response: response, Messages: messages, Usage: usage,
				FinishReason: response.FinishReason,
				ModelCalls:   counts.modelCalls, ToolExecutions: counts.toolExecutions, Pending: pending}, nil
		}
	}
}

// partialOutcome packages the evidence a failed loop accumulated, so a
// run that errors keeps its transcript, usage, activity counts, and the
// last completed turn's terminal cause.
func partialOutcome(messages []model.Message, usage model.Usage, counts runCounts, finish model.FinishReason) Outcome {
	return Outcome{Messages: messages, Usage: usage, FinishReason: finish,
		ModelCalls: counts.modelCalls, ToolExecutions: counts.toolExecutions}
}

type toolCallResult[Deps any] struct {
	call     model.ToolCall
	declared tool.Tool[Deps]
	result   string
	err      error
}

// runToolCalls validates every requested call, executes them either in
// emission order or concurrent barrier-separated groups, then emits results
// in emission order regardless of completion order. A call whose execution
// returns a *tool.Deferred sentinel is not executed to a result: it is
// recorded as pending, reported with EventDeferred, and the batch's other
// calls still run. The returned messages carry results for the executed
// calls only; pending lists every deferred call in emission order.
func runToolCalls[Deps any](ctx context.Context, tools []tool.Tool[Deps], deps Deps, calls []model.ToolCall, config ToolConfig, feedbacks map[string]int, counts *runCounts, turn int, emit Observer) ([]model.Message, []PendingCall, error) {
	results := make([]toolCallResult[Deps], len(calls))
	for i, call := range calls {
		declared, ok := findTool(tools, call.Name)
		if !ok {
			return nil, nil, &ToolError{ToolName: call.Name, CallID: call.ID, Err: fmt.Errorf("tool was not declared for this run")}
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
		runToolGroup(ctx, results[start:end], deps, config.DefaultTimeout, config.Parallel && end-start > 1, counts, turn, emit)
		start = end
	}
	messages := make([]model.Message, 0, len(results))
	var pending []PendingCall
	for _, item := range results {
		var deferred *tool.Deferred
		if item.err != nil && errors.As(item.err, &deferred) {
			if deferred.Kind != tool.DeferApproval && deferred.Kind != tool.DeferExternal {
				return nil, nil, &ToolError{ToolName: item.call.Name, CallID: item.call.ID,
					Err: fmt.Errorf("tool deferred without a kind: %w", item.err)}
			}
			pending = append(pending, PendingCall{Call: item.call, Defer: *deferred})
			continue
		}
		if item.err == nil {
			messages = append(messages, model.Message{Role: model.RoleTool, ToolCallID: item.call.ID, ToolName: item.call.Name, Content: item.result})
			continue
		}
		// Cancellation and deadlines keep their identity through the
		// ToolError chain — errors.Is still matches them — while gaining
		// the tool stage, so a run stopped inside a tool reports where it
		// stopped.
		if errors.Is(item.err, context.Canceled) || errors.Is(item.err, context.DeadlineExceeded) {
			return nil, nil, &ToolError{ToolName: item.call.Name, CallID: item.call.ID, Err: item.err}
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
		return nil, nil, &ToolError{ToolName: item.call.Name, CallID: item.call.ID, Err: terminalToolError(feedbacks[item.declared.Name], item.err)}
	}
	return messages, pending, nil
}

// runToolGroup executes one sequential or barrier-separated group,
// emitting tool events around each execution. Sequential groups emit
// start and end per call; parallel groups emit every start in emission
// order before any call runs and every end in the same order after the
// group completes, so event order stays deterministic. A call that
// returns a deferred sentinel reports EventDeferred instead of a tool
// end and does not count as an execution.
func runToolGroup[Deps any](ctx context.Context, group []toolCallResult[Deps], deps Deps, timeout time.Duration, parallel bool, counts *runCounts, turn int, emit Observer) {
	if !parallel {
		for i := range group {
			if err := ctx.Err(); err != nil {
				group[i].err = err
				return
			}
			emitEvent(emit, Event{Kind: EventToolStart, Turn: turn, CallID: group[i].call.ID, ToolName: group[i].call.Name, Args: group[i].call.Args})
			group[i].result, group[i].err = executeTool(ctx, group[i].declared, deps, group[i].call.Args, timeout)
			if deferredEvent(emit, group[i], turn) {
				continue
			}
			counts.toolExecutions++
			event := Event{Kind: EventToolEnd, Turn: turn, CallID: group[i].call.ID, ToolName: group[i].call.Name, Result: group[i].result}
			if group[i].err != nil {
				event.Err = group[i].err
			}
			emitEvent(emit, event)
		}
		return
	}
	for i := range group {
		emitEvent(emit, Event{Kind: EventToolStart, Turn: turn, CallID: group[i].call.ID, ToolName: group[i].call.Name, Args: group[i].call.Args})
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
	for i := range group {
		if deferredEvent(emit, group[i], turn) {
			continue
		}
		counts.toolExecutions++
		event := Event{Kind: EventToolEnd, Turn: turn, CallID: group[i].call.ID, ToolName: group[i].call.Name, Result: group[i].result}
		if group[i].err != nil {
			event.Err = group[i].err
		}
		emitEvent(emit, event)
	}
}

// deferredEvent reports and emits EventDeferred for a result whose error
// carries a deferred sentinel: true when the call deferred, false when it
// produced an ordinary outcome. It never rewrites item.err — the batch
// processor needs the sentinel to record the pending call.
func deferredEvent[Deps any](emit Observer, item toolCallResult[Deps], turn int) bool {
	var deferred *tool.Deferred
	if item.err == nil || !errors.As(item.err, &deferred) {
		return false
	}
	emitEvent(emit, Event{Kind: EventDeferred, Turn: turn, CallID: item.call.ID, ToolName: item.call.Name, Args: item.call.Args})
	return true
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

// ExecuteApprovedTool re-runs one approved deferred call: the tool
// executes under tool.WithApprovedCall so CallApproved reports true, with
// the same narrowest-deadline policy as loop executions. It is the resume
// path for approval-kind pending calls; callers surface failures as tool
// errors.
func ExecuteApprovedTool[Deps any](ctx context.Context, declared tool.Tool[Deps], deps Deps, args json.RawMessage, defaultTimeout time.Duration) (string, error) {
	return executeTool(tool.WithApprovedCall(ctx), declared, deps, args, defaultTimeout)
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
// retry.MaxAttempts with context-aware waits between attempts, and reports
// the provider calls consumed, attempts included. A context error always
// wins over the model error: cancellation is returned raw, never retried
// or wrapped.
func generate(ctx context.Context, m model.Model, request model.Request, retry RetryConfig, turn int, emit Observer) (model.Response, int, error) {
	for attempt := 1; ; attempt++ {
		emitEvent(emit, Event{Kind: EventModelStart, Turn: turn, Attempt: attempt})
		response, err := m.Generate(ctx, request)
		event := Event{Kind: EventModelEnd, Turn: turn, Attempt: attempt, Usage: response.Usage}
		if err != nil {
			event.Err = err
		}
		emitEvent(emit, event)
		if err == nil {
			return response, attempt, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return model.Response{}, attempt, ctxErr
		}
		if attempt >= retry.MaxAttempts || !model.IsRetryable(err) {
			return model.Response{}, attempt, terminalModelError(attempt, err)
		}
		delay := time.Duration(0)
		if retry.Backoff != nil {
			delay = retry.Backoff(attempt)
		}
		if err := waitFor(ctx, delay); err != nil {
			return model.Response{}, attempt, err
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
