package golem

import (
	"encoding/json"
	"fmt"

	"github.com/abubakarsiddik31/golem/model"
)

// Stage identifies the run phase that returned an error.
type Stage string

const (
	// StageModel means the model could not generate a response.
	StageModel Stage = "model"
	// StageDecode means a generated response could not become the declared type.
	StageDecode Stage = "decode"
	// StageTool means a tool execution failed; the run aborted.
	StageTool Stage = "tool"
	// StageLoop means the run exceeded its model-turn limit before producing
	// a final response.
	StageLoop Stage = "loop"
	// StageUsage means the run crossed a configured usage bound.
	StageUsage Stage = "usage"
	// StageCanceled means a tool ended the run on purpose by returning
	// &tool.Canceled — a deliberate stop, not a failure. The sentinel is
	// reachable through RunError.Unwrap with errors.As, and RunError.Partial
	// preserves the evidence, including the tool results recorded before
	// the stop.
	StageCanceled Stage = "canceled"
	// StagePolicy means an output guard blocked the run's final output
	// (ADR 0042): the model produced something the registered policy
	// refuses to expose. The *guard.Blocked is in the chain through
	// RunError.Unwrap.
	StagePolicy Stage = "policy"
	// StageContentFilter means the provider's safety filter stopped the
	// model response and the agent was configured to treat that as an
	// error with WithContentFilterError. The *ContentFilterError is in
	// the chain through RunError.Unwrap.
	StageContentFilter Stage = "content_filter"
	// StageLoopDetected means the run repeated one tool call beyond the
	// bound configured with WithLoopDetection. The *LoopDetectedError is
	// in the chain through RunError.Unwrap.
	StageLoopDetected Stage = "loop_detected"
)

// RunError adds an inspectable execution stage while preserving the source
// error for errors.Is and errors.As.
type RunError struct {
	Stage Stage
	Err   error
	// Partial preserves the evidence the run accumulated before the
	// error ended it; see PartialResult. It is nil when the run failed
	// before producing any: no model turn completed, no usage was
	// reported, and no tool executed.
	Partial *PartialResult
}

func (e *RunError) Error() string {
	return fmt.Sprintf("golem: %s stage: %v", e.Stage, e.Err)
}

// Unwrap exposes the originating model or decoder error.
func (e *RunError) Unwrap() error {
	return e.Err
}

// ContentFilterError reports a model response the provider's safety
// filter stopped. It is returned only when the agent is configured with
// WithContentFilterError; without that option the response is ordinary
// output and its finish reason is reported on Result.FinishReason.
//
// Message is the filtered assistant response as the provider returned
// it. Content may be empty or partial — providers disagree on whether
// refusal text accompanies a content-filter stop — and it is preserved
// as evidence rather than discarded.
type ContentFilterError struct {
	// Message is the filtered assistant response.
	Message model.Message
}

func (e *ContentFilterError) Error() string {
	return "golem: model response stopped by the provider's content filter"
}

// LoopPattern identifies the repetition a loop policy detected.
type LoopPattern string

const (
	// LoopRepeatedCall is the same tool call requested too many times.
	LoopRepeatedCall LoopPattern = "repeated_call"
	// LoopRepeatedResult is the same call returning the same result.
	LoopRepeatedResult LoopPattern = "repeated_result"
	// LoopAlternatingCalls is an A-B-A-B call pattern.
	LoopAlternatingCalls LoopPattern = "alternating_calls"
	// LoopRepeatedText is the same final text returned repeatedly.
	LoopRepeatedText LoopPattern = "repeated_text"
)

// LoopAction selects what a detected loop does: stop the run, or nudge
// the model and continue.
type LoopAction int

const (
	// LoopStop ends the run with a *LoopDetectedError.
	LoopStop LoopAction = iota
	// LoopNudge feeds a course-correction back to the model and
	// continues, bounded by the run's iteration and usage limits.
	LoopNudge
)

// LoopPolicy configures repeated-call, repeated-result, alternating-call,
// and repeated-text detection. Zero thresholds disable a check; Window
// bounds how many recent calls the call-based checks inspect (zero uses
// 10). Action selects stop or nudge.
type LoopPolicy struct {
	Window           int
	RepeatedCall     int
	RepeatedResult   int
	AlternatingCalls int
	RepeatedText     int
	Action           LoopAction
}

// LoopDetectedError reports a loop a policy detected. For a stopping
// action it ends the run; the fields describe the repetition.
//
// Args holds the offending call's arguments for call-based patterns,
// Count is how many appearances were counted, and Threshold is the
// configured bound. Calls compare by tool name and canonical JSON, so
// object key order does not hide a repeat.
type LoopDetectedError struct {
	// Pattern is the detected repetition.
	Pattern LoopPattern
	// ToolNames are the tools involved, one or two for call patterns
	// and empty for repeated text.
	ToolNames []string
	// ToolName is the first involved tool, kept for compatibility with
	// the repeated-call shorthand.
	ToolName string
	// Args is the offending call's raw arguments for call patterns.
	Args json.RawMessage
	// Count is the number of appearances, including the failed one.
	Count int
	// Threshold is the configured maximum allowed.
	Threshold int
}

func (e *LoopDetectedError) Error() string {
	if e.ToolName != "" {
		return fmt.Sprintf("golem: %s loop on tool %q (%d, limit %d)", e.Pattern, e.ToolName, e.Count, e.Threshold)
	}
	return fmt.Sprintf("golem: %s loop (%d, limit %d)", e.Pattern, e.Count, e.Threshold)
}

// PartialResult is the evidence of a failed run: the conversation through
// its last completed model turn, the usage completed turns reported, and
// the run's activity counts. A failure inside a tool batch leaves
// Messages ending at the assistant turn that requested the batch — its
// executed results are observable through run events — and any tool call
// left without a result is repaired on resume, exactly as for a crashed
// run. A cancellation is the one exception: the tool results recorded
// before the stop stay in Messages, every unanswered call of the batch is
// closed with a synthesized no-result result, and the transcript is
// resumable without repair. Feed Partial.Messages to RunWithHistory to
// continue a failed conversation.
type PartialResult struct {
	// Messages is the ordered conversation evidence, ending at the last
	// completed model turn.
	Messages []model.Message
	// Usage sums the provider-reported consumption of completed turns.
	Usage model.Usage
	// FinishReason is the provider's terminal cause of the last
	// completed model turn — FinishLength, for example, when a
	// truncated turn is what made the output undecodable.
	FinishReason model.FinishReason
	// Requests counts model calls the run made, failed attempts included.
	Requests int
	// ToolCalls counts tool executions the run attempted.
	ToolCalls int
	// Cost is the run's cumulative usage priced at the agent's WithPrice
	// rates when one is wired; zero when no price is wired.
	Cost float64
	// RunID and ConversationID are the failed run's identity — the same
	// values its events carried and its Result would have reported — so
	// telemetry can join a failure to the conversation it interrupted
	// even when the failure predates any recoverable evidence.
	RunID          string
	ConversationID string
}

// check reports whether cumulative usage crossed any configured bound.
// A zero limit is a no-op: only positive bounds are enforced.
func (l UsageLimit) check(usage model.Usage, modelCalls, toolExecutions int, cost float64) error {
	var crossed *UsageLimitError
	switch {
	case l.InputTokens > 0 && usage.InputTokens > l.InputTokens:
		crossed = &UsageLimitError{
			Kind:   "input token",
			Limit:  float64(l.InputTokens),
			Actual: float64(usage.InputTokens),
		}
	case l.OutputTokens > 0 && usage.OutputTokens > l.OutputTokens:
		crossed = &UsageLimitError{
			Kind:   "output token",
			Limit:  float64(l.OutputTokens),
			Actual: float64(usage.OutputTokens),
		}
	case l.TotalTokens > 0 && usage.InputTokens+usage.OutputTokens > l.TotalTokens:
		crossed = &UsageLimitError{
			Kind:   "total token",
			Limit:  float64(l.TotalTokens),
			Actual: float64(usage.InputTokens + usage.OutputTokens),
		}
	case l.Requests > 0 && modelCalls > l.Requests:
		crossed = &UsageLimitError{
			Kind:   "request",
			Limit:  float64(l.Requests),
			Actual: float64(modelCalls),
		}
	case l.ToolCalls > 0 && toolExecutions > l.ToolCalls:
		crossed = &UsageLimitError{
			Kind:   "tool call",
			Limit:  float64(l.ToolCalls),
			Actual: float64(toolExecutions),
		}
	case l.Cost > 0 && cost > l.Cost:
		crossed = &UsageLimitError{
			Kind:   "cost",
			Limit:  l.Cost,
			Actual: cost,
		}
	}
	if crossed == nil {
		return nil
	}
	return crossed
}

// UsageLimitError reports that a run's cumulative usage crossed one of its
// configured bounds. It is wrapped in a RunError with the usage stage.
type UsageLimitError struct {
	// Kind names the crossed dimension, e.g. "output token".
	Kind string
	// Limit is the configured bound. Token and activity limits are whole
	// numbers; Cost is a USD amount.
	Limit float64
	// Actual is the run's cumulative value when the run failed.
	Actual float64
}

func (e *UsageLimitError) Error() string {
	return fmt.Sprintf("run exceeded the %s limit of %g (used %g)", e.Kind, e.Limit, e.Actual)
}
