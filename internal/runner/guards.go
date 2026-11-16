package runner

import (
	"context"
	"errors"
	"fmt"
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
		limit := config.OutputLimit
		if item.declared.MaxOutputBytes > 0 {
			limit = item.declared.MaxOutputBytes
		}
		if limit > 0 {
			result.Text = truncateOutput(result.Text, limit)
		}
	}
	item.result, item.err = result, err
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
