package golem

import (
	"context"
	"fmt"

	"github.com/abubakarsiddik31/golem/model"
)

// HistoryProcessor rewrites the history of one run before the request is
// built. It receives the history exactly as the caller supplied it —
// before validation and repair — and returns the history to send; the
// returned messages are then part-validated, repaired, and sent. The
// processor runs once per run; an error fails the run before any model
// call. Processors must be deterministic enough for their caller's
// purposes: nothing re-runs them.
type HistoryProcessor func(ctx context.Context, history []model.Message) ([]model.Message, error)

// WithHistoryProcessor configures a processor applied to the history of
// every run, before validation and repair, on Run and its history-aware
// and streaming variants. See TrimHistory for a builtin.
func WithHistoryProcessor[Deps any, Output any](processor HistoryProcessor) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.historyProcessor = processor
	}
}

// TrimHistory returns a HistoryProcessor that keeps the newest
// maxMessages messages of a conversation. After the cut it advances past
// messages that cannot open a request: tool results whose requesting call
// was trimmed, and assistant turns carrying tool calls whose results were
// trimmed — repair would otherwise reattach synthesized results, paying
// tokens for evidence the trim meant to drop. A budget below 1, or a
// history with nothing left after the boundary rule, fails the run.
func TrimHistory(maxMessages int) HistoryProcessor {
	return func(ctx context.Context, history []model.Message) ([]model.Message, error) {
		if maxMessages < 1 {
			return nil, fmt.Errorf("golem: TrimHistory budget must be at least 1, got %d", maxMessages)
		}
		if len(history) <= maxMessages {
			return history, nil
		}
		kept := history[len(history)-maxMessages:]
		for len(kept) > 0 && !opensConversation(kept[0]) {
			kept = kept[1:]
		}
		if len(kept) == 0 {
			return nil, fmt.Errorf("golem: TrimHistory left no messages; history of %d messages has no turn that can open a request", len(history))
		}
		return kept, nil
	}
}

// opensConversation reports whether a message can start a request
// without its conversation prefix: user and plain-assistant turns can;
// tool results cannot (their call is gone), and assistant turns carrying
// tool calls cannot (their results are gone).
func opensConversation(message model.Message) bool {
	switch {
	case message.Role == model.RoleTool:
		return false
	case message.Role == model.RoleAssistant && len(message.ToolCalls) > 0:
		return false
	default:
		return true
	}
}
