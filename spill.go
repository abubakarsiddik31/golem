package golem

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abubakarsiddik31/golem/tool"
)

// SpillReadToolName is the tool an agent registers when tool output
// spilling is enabled. The model calls it with a handle from a spill
// marker to read the stored payload in bounded slices.
const SpillReadToolName = "read_tool_result"

// ToolOutputSummaryFunc condenses an oversized tool result before the
// model sees it. toolName identifies the tool that produced text; the
// function owns any model call and must honor ctx. Returning an error
// falls back to the configured output limit.
type ToolOutputSummaryFunc func(ctx context.Context, toolName, text string) (string, error)

// WithToolOutputSpill stores tool result text at or above threshold bytes
// in store, replacing it with a marker that names the handle, the total
// size, and a preview. The agent registers SpillReadToolName so the model
// can page the full payload back in bounded slices. If a store write
// fails, the result falls back to WithToolOutputLimit. Spill and summary
// modes are mutually exclusive. threshold must be at least 1, and store
// is required.
func WithToolOutputSpill[Deps any, Output any](store tool.SpillStore, threshold int) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.spillStore = store
		agent.spillThreshold = threshold
		agent.spillSet = true
	}
}

// WithToolOutputSummary condenses tool result text at or above threshold
// bytes with fn, before it enters history. A nil or empty summary, or a
// function error, falls back to WithToolOutputLimit. Spill and summary
// modes are mutually exclusive. threshold must be at least 1, and fn is
// required.
func WithToolOutputSummary[Deps any, Output any](fn ToolOutputSummaryFunc, threshold int) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.summaryFn = fn
		agent.summaryThreshold = threshold
		agent.summarySet = true
	}
}

var spillReadSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"handle": {"type": "string", "description": "Handle from a tool-output spill marker."},
		"offset": {"type": "integer", "minimum": 0, "description": "Byte offset to start reading from; omit for the start."},
		"limit": {"type": "integer", "minimum": 1, "description": "Maximum bytes to return; the tool clamps it."}
	},
	"required": ["handle"],
	"additionalProperties": false
}`)

func (a *Agent[Deps, Output]) spillReadTool() (tool.Tool[Deps], error) {
	return tool.New(tool.Tool[Deps]{
		Name:        SpillReadToolName,
		Description: "Read a bounded slice of a tool result that was spilled to storage. Use the handle from the spill marker.",
		Schema:      spillReadSchema,
		Exec: func(ctx context.Context, _ Deps, args json.RawMessage) (tool.Result, error) {
			var input struct {
				Handle string `json:"handle"`
				Offset int    `json:"offset"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return tool.Result{}, &tool.Failed{Reason: "invalid arguments: " + err.Error()}
			}
			if strings.TrimSpace(input.Handle) == "" {
				return tool.Result{}, &tool.Failed{Reason: "handle is required"}
			}
			if input.Offset < 0 {
				return tool.Result{}, &tool.Failed{Reason: "offset must not be negative"}
			}
			if input.Limit < 0 {
				return tool.Result{}, &tool.Failed{Reason: "limit must not be negative"}
			}
			text, total, err := a.spillStore.Read(ctx, input.Handle, input.Offset, input.Limit)
			if err == tool.ErrSpillNotFound {
				return tool.Result{}, &tool.Failed{Reason: "no spilled result for handle " + input.Handle}
			}
			if err != nil {
				return tool.Result{}, fmt.Errorf("golem: read spill %s: %w", input.Handle, err)
			}
			if input.Offset == 0 && len(text) == total {
				return tool.Text(text), nil
			}
			return tool.Text(fmt.Sprintf("%s\n\n[read bytes %d-%d of %d]", text, input.Offset, input.Offset+len(text), total)), nil
		},
	})
}
