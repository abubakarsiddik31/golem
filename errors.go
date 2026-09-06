package golem

import (
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

// PartialResult is the evidence of a failed run: the conversation through
// its last completed model turn, the usage completed turns reported, and
// the run's activity counts. A failure inside a tool batch leaves
// Messages ending at the assistant turn that requested the batch — its
// executed results are observable through run events — and any tool call
// left without a result is repaired on resume, exactly as for a crashed
// run. Feed Partial.Messages to RunWithHistory to continue a failed
// conversation.
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
}

// check reports whether cumulative usage crossed any configured bound.
// A zero limit is a no-op: only positive bounds are enforced.
func (l UsageLimit) check(usage model.Usage, modelCalls, toolExecutions int) error {
	var crossed *UsageLimitError
	switch {
	case l.InputTokens > 0 && usage.InputTokens > l.InputTokens:
		crossed = &UsageLimitError{
			Kind:   "input token",
			Limit:  l.InputTokens,
			Actual: usage.InputTokens,
		}
	case l.OutputTokens > 0 && usage.OutputTokens > l.OutputTokens:
		crossed = &UsageLimitError{
			Kind:   "output token",
			Limit:  l.OutputTokens,
			Actual: usage.OutputTokens,
		}
	case l.TotalTokens > 0 && usage.InputTokens+usage.OutputTokens > l.TotalTokens:
		crossed = &UsageLimitError{
			Kind:   "total token",
			Limit:  l.TotalTokens,
			Actual: usage.InputTokens + usage.OutputTokens,
		}
	case l.Requests > 0 && modelCalls > l.Requests:
		crossed = &UsageLimitError{
			Kind:   "request",
			Limit:  l.Requests,
			Actual: modelCalls,
		}
	case l.ToolCalls > 0 && toolExecutions > l.ToolCalls:
		crossed = &UsageLimitError{
			Kind:   "tool call",
			Limit:  l.ToolCalls,
			Actual: toolExecutions,
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
	// Limit is the configured bound.
	Limit int
	// Actual is the run's cumulative value when the run failed.
	Actual int
}

func (e *UsageLimitError) Error() string {
	return fmt.Sprintf("run exceeded the %s limit of %d (used %d)", e.Kind, e.Limit, e.Actual)
}
