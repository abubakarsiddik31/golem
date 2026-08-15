package golem

import "fmt"

// Stage identifies the run phase that returned an error.
type Stage string

const (
	// StageModel means the model could not generate a response.
	StageModel Stage = "model"
	// StageDecode means a generated response could not become the declared type.
	StageDecode Stage = "decode"
	// StageTool means a tool execution failed; the run aborted per ADR 0002.
	StageTool Stage = "tool"
	// StageLoop means the run exceeded its model-turn limit before producing
	// a final response.
	StageLoop Stage = "loop"
)

// RunError adds an inspectable execution stage while preserving the source
// error for errors.Is and errors.As.
type RunError struct {
	Stage Stage
	Err   error
}

func (e *RunError) Error() string {
	return fmt.Sprintf("golem: %s stage: %v", e.Stage, e.Err)
}

// Unwrap exposes the originating model or decoder error.
func (e *RunError) Unwrap() error {
	return e.Err
}
