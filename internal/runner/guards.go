package runner

import (
	"context"
	"errors"
	"time"

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
	item.result, item.err = result, err
}
