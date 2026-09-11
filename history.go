package golem

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/abubakarsiddik31/golem/internal/runner"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tokens"
)

// HistoryRepair reports what one NormalizeHistory pass changed, so an
// application boundary can act on a damaged history instead of
// discovering it from a provider rejection.
type HistoryRepair = runner.HistoryRepair

// NormalizeHistory restores the call/result pairing providers require
// and reports every change. A crashed or cancelled run leaves tool calls
// without results — each receives a synthesized interrupted result,
// placed directly after the assistant message that requested it. A
// context-evicting pipeline leaves results without calls — each is
// dropped. A stream that died mid-arguments leaves a tool call whose
// JSON is not an object — reported as truncated; the arguments stay
// verbatim in the returned history, and adapters make them sendable on
// the wire.
//
// The pass only adds or removes pairing evidence; it never rewrites
// messages. Synthesized and Dropped name what this pass changed;
// Truncated names the damage the pass leaves in place by design — a
// truncated call stays listed on every pass. The pass is deterministic
// and idempotent — normalizing a normalized history returns it unchanged
// — and synthesized results carry no wall-clock data, so repeated passes
// stay prompt-cache friendly. Runs also self-heal pairing at request
// build time, silently; NormalizeHistory is the explicit pass for
// application boundaries that want to see the damage first.
func NormalizeHistory(history []model.Message) ([]model.Message, HistoryRepair) {
	return runner.RepairHistoryWithReport(history)
}

// SanitizeReport records what one SanitizeHistory pass changed, so an
// application endpoint can log, reject, or otherwise act on what an
// untrusted client tried to assert instead of discovering it from a
// provider rejection or a fabricated approval.
type SanitizeReport struct {
	// SystemPrompts counts the system messages the pass dropped. Runs
	// never send history system prompts — instructions govern every run
	// — so the count reports an attempt, not a model-visible change.
	SystemPrompts int
	// UnsafeParts lists, in history order, the URL parts dropped for a
	// scheme other than http or https.
	UnsafeParts []UnsafePart
	// Repair is the pairing repair the pass applied — the same report
	// NormalizeHistory produces — so a fabricated dangling call is named
	// here rather than surfacing as a resume-time surprise.
	Repair HistoryRepair
}

// UnsafePart is one dropped URL part: the threat is where the URL asks
// the provider to look, not what the part shows.
type UnsafePart struct {
	// MessageIndex is the part's message in the history exactly as it
	// was passed to SanitizeHistory.
	MessageIndex int
	// Kind is the dropped part's kind.
	Kind model.PartKind
	// Scheme is the rejected URL scheme, lowercased; empty when the URL
	// had no parsable scheme.
	Scheme string
}

// SanitizeHistory makes an untrusted history safe to run: it drops
// system messages, drops URL parts whose scheme is not http or https
// (case-insensitive; unparsable and scheme-relative URLs included),
// repairs call/result pairing, and reports every change. Use it at the
// trust boundary — a browser request resuming a conversation, another
// service's transcript, a client-submitted paused run — before handing
// the history to Run, RunWithHistory, or RunWithDeferredResults.
//
// The pass never rewrites content: thinking blocks, failure flags, call
// arguments, and identity stamps pass through untouched, and
// inline-data parts are left to the existing part validation. A user
// message left with neither content nor parts — by the drops or as
// submitted — is removed; tool results are never removed, because
// their pairing evidence must survive. The pass is deterministic and
// idempotent: sanitizing a sanitized history returns it unchanged with
// a zero report.
//
// Sanitization narrows what a fabricated history can reach; it does
// not make one trustworthy. Authenticate at the transport, scope the
// toolset to the caller, and re-validate high-stakes effects against
// server-side state inside the tool — see the conversations guide's
// trust rules. Runs never sanitize automatically: the boundary is the
// application's, and trusted server-side history has nothing to strip.
func SanitizeHistory(history []model.Message) ([]model.Message, SanitizeReport) {
	var report SanitizeReport
	sanitized := make([]model.Message, 0, len(history))
	for i, message := range history {
		if message.Role == model.RoleSystem {
			report.SystemPrompts++
			continue
		}
		message = dropUnsafeParts(message, i, &report)
		if message.Role == model.RoleUser && message.Content == "" && len(message.Parts) == 0 {
			continue
		}
		sanitized = append(sanitized, message)
	}
	repaired, repair := runner.RepairHistoryWithReport(sanitized)
	report.Repair = repair
	return repaired, report
}

// dropUnsafeParts returns the message with non-http(s) URL parts
// removed, reporting each drop against the message's index in the
// history as passed. The input part slice is never mutated.
func dropUnsafeParts(message model.Message, index int, report *SanitizeReport) model.Message {
	if len(message.Parts) == 0 {
		return message
	}
	kept := make([]model.Part, 0, len(message.Parts))
	for _, part := range message.Parts {
		if part.URL == "" {
			kept = append(kept, part)
			continue
		}
		if scheme := urlScheme(part.URL); scheme == "http" || scheme == "https" {
			kept = append(kept, part)
			continue
		}
		report.UnsafeParts = append(report.UnsafeParts, UnsafePart{
			MessageIndex: index, Kind: part.Kind, Scheme: urlScheme(part.URL),
		})
	}
	message.Parts = kept
	return message
}

// urlScheme parses the scheme of a raw URL, lowercased; empty when the
// URL has none it admits to.
func urlScheme(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Scheme)
}

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

// BudgetHistory returns a HistoryProcessor that keeps the newest turns
// of a conversation whose input-token count fits maxTokens, as reported
// by the counter. It counts the history it receives — the run's tools
// and instructions are not included, so leave headroom for them — and,
// while over budget, drops the oldest message and counts again, always
// advancing past messages that cannot open a request under the same
// boundary rule TrimHistory uses. Counting is one call to the counter
// per dropped message plus one, so prefer a generous budget over a
// tight one. A nil counter or a budget below 1 fails the run; so does a
// history whose newest openable turn alone exceeds the budget, or
// nothing left after the boundary rule.
func BudgetHistory(counter tokens.Counter, maxTokens int) HistoryProcessor {
	return func(ctx context.Context, history []model.Message) ([]model.Message, error) {
		if counter == nil {
			return nil, fmt.Errorf("golem: BudgetHistory requires a non-nil counter")
		}
		if maxTokens < 1 {
			return nil, fmt.Errorf("golem: BudgetHistory budget must be at least 1, got %d", maxTokens)
		}
		drop := 0
		for {
			for drop < len(history) && !opensConversation(history[drop]) {
				drop++
			}
			kept := history[drop:]
			if len(kept) == 0 {
				return nil, fmt.Errorf("golem: BudgetHistory left no messages; history of %d messages has no turn that can open a request", len(history))
			}
			count, err := counter.CountTokens(ctx, tokens.CountInput{Messages: kept})
			if err != nil {
				return nil, fmt.Errorf("golem: BudgetHistory: %w", err)
			}
			if count <= maxTokens {
				return kept, nil
			}
			if len(kept) == 1 {
				return nil, fmt.Errorf("golem: BudgetHistory: newest openable turn alone needs %d tokens, over budget %d", count, maxTokens)
			}
			drop++
		}
	}
}
