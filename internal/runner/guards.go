package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abubakarsiddik31/golem/guard"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// resolveCall takes one tool call from validation to outcome: the
// argument guards run first (a blocked or rejected call never
// executes), then the tool runs under its deadline, then the result
// guards run over a successful result. The item records the outcome;
// guarded items do not count toward tool activity.
func resolveCall[Deps any](ctx context.Context, item *toolCallResult[Deps], deps Deps, config ToolConfig, timeout time.Duration) {
	call, guardErr := guard.ApplyToolCall(ctx, config.ToolGuards, item.call)
	if guardErr != nil {
		item.guarded = true
		var retry *model.ModelRetry
		if errors.As(guardErr, &retry) {
			item.err = retry
			return
		}
		var blocked *guard.Blocked
		if errors.As(guardErr, &blocked) {
			item.result = tool.Text(guard.Refusal(blocked.Reason))
			return
		}
		item.err = guardErr
		return
	}
	item.call = call
	result, err := executeTool(ctx, item.declared, deps, item.call.Args, timeout)
	if err == nil && len(config.ResultGuards) > 0 {
		if guarded, blocked := guard.ApplyToolResult(ctx, config.ResultGuards, item.call, result); blocked != nil {
			item.guarded = true
			result = guarded
		}
	}
	if err == nil {
		result.Text = applyOutputPolicy(ctx, item.declared.Name, item.declared.MaxOutputBytes, result.Text, config)
	}
	item.result, item.err = result, err
}

const spillReadToolName = "read_tool_result"

// applyOutputPolicy reduces one successful result in the configured
// order: lossless spill, then summary, then truncation. The spill-read
// tool is exempt so reading a spilled payload cannot spill again.
func applyOutputPolicy(ctx context.Context, toolName string, toolLimit int, text string, config ToolConfig) string {
	if toolName == spillReadToolName {
		return text
	}
	if config.Spill != nil && config.Spill.Put != nil && len(text) >= config.Spill.Threshold {
		if handle, err := config.Spill.Put(ctx, text); err == nil && handle != "" {
			return spillMarker(handle, len(text), text)
		}
	}
	if config.Summary != nil && config.Summary.Summarize != nil && len(text) >= config.Summary.Threshold {
		if summary, err := config.Summary.Summarize(ctx, toolName, text); err == nil && strings.TrimSpace(summary) != "" {
			text = summary
		}
	}
	limit := config.OutputLimit
	if toolLimit > 0 {
		limit = toolLimit
	}
	if limit > 0 {
		return truncateOutput(text, limit)
	}
	return text
}

func spillMarker(handle string, total int, text string) string {
	return fmt.Sprintf("[tool output spilled: handle %q, %d bytes total; preview follows]\n\n%s\n\n[call read_tool_result with this handle to read more]", handle, total, previewText(text, 300))
}

func previewText(text string, max int) string {
	if len(text) <= max {
		return text
	}
	end := retreatRune(text, max)
	return text[:end]
}

// truncateOutput keeps the head and tail of an oversized tool result and
// marks the cut. The marker names the original size, so the model knows
// how much it is missing; the result never exceeds limit bytes and never
// splits a UTF-8 rune.
func truncateOutput(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	marker := fmt.Sprintf("\n\n[tool output truncated; original size %d bytes]", len(text))
	budget := limit - len(marker)
	if budget <= 0 {
		return truncateRunes(text, limit)
	}
	head := budget * 2 / 3
	tail := budget - head
	headEnd := retreatRune(text, head)
	tailStart := advanceRune(text, len(text)-tail)
	return text[:headEnd] + marker + text[tailStart:]
}

func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	return text[:retreatRune(text, limit)]
}

func retreatRune(text string, index int) int {
	for index > 0 && index < len(text) && !utf8.RuneStart(text[index]) {
		index--
	}
	return index
}

func advanceRune(text string, index int) int {
	for index > 0 && index < len(text) && !utf8.RuneStart(text[index]) {
		index++
	}
	return index
}
