// Package guard provides typed guard functions a run calls at its
// boundaries: the user prompt before the first model call, each tool
// call's arguments before execution and each tool result before the
// model sees it, and the final assistant text before decoding. A guard
// returns a Result whose action allows, blocks, replaces, or — for
// tool arguments — requests correction of the guarded value.
//
// Guards are guardrails against accidents, not a security boundary:
// blocks are graceful, the model sees the refusal, and no
// configuration here isolates anything. See ADR 0042.
package guard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// Action is one guard outcome.
type Action uint8

const (
	// Allow passes the value on: to the next guard in the chain, or to
	// the boundary itself when the guard is last.
	ActionAllow Action = iota
	// Block stops the value. What a block does depends on the boundary:
	// a blocked prompt becomes a synthesized refusal, a blocked tool
	// call or result becomes a synthetic tool message the model reads,
	// and a blocked output fails the run.
	ActionBlock
	// Replace substitutes the Result's Text for the guarded value. The
	// guards after it in the chain see the replacement, and for tool
	// arguments the replacement is the raw JSON passed to the tool.
	ActionReplace
	// Retry asks for correction of tool arguments: the run delivers the
	// reason as the call's rejection and the model can call the tool
	// again within the WithToolRetries budget. Retry is invalid at the
	// other boundaries — an invalid verdict blocks (fail-closed).
	ActionRetry
)

// Result is a guard's verdict on one guarded value.
type Result struct {
	// Action is the outcome. Zero-value Results allow.
	Action Action
	// Reason explains a Block or Retry. It reaches the model (tool
	// boundaries) or the caller (input, output).
	Reason string
	// Text is the replacement value for Replace: the substitute prompt
	// or output text, or the substitute tool arguments as raw JSON.
	Text string
}

// Allow returns a verdict that passes the value on.
func Allow() Result { return Result{} }

// Block returns a verdict that stops the value with a reason.
func Block(reason string) Result { return Result{Action: ActionBlock, Reason: reason} }

// Replace returns a verdict that substitutes text for the value.
func Replace(text string) Result { return Result{Action: ActionReplace, Text: text} }

// Retry returns a verdict that asks the model to correct tool
// arguments.
func Retry(reason string) Result { return Result{Action: ActionRetry, Reason: reason} }

// Blocked reports a guard verdict that stopped a guarded value. A
// blocked tool call or result carries the refusal the model sees; a
// blocked output reaches the caller inside a RunError at the policy
// stage.
type Blocked struct {
	// Reason is why the guard stopped the value.
	Reason string
}

func (e *Blocked) Error() string {
	if e.Reason == "" {
		return "guard: blocked"
	}
	return "guard: blocked: " + e.Reason
}

// InputGuard inspects the run's prompt before the model sees it.
type InputGuard func(ctx context.Context, prompt string) Result

// ToolGuard inspects one tool call's arguments before the tool runs.
type ToolGuard func(ctx context.Context, call model.ToolCall) Result

// ResultGuard inspects one successful tool result before the model
// sees it. The tool has already run: blocking its result does not undo
// its side effects.
type ResultGuard func(ctx context.Context, call model.ToolCall, result tool.Result) Result

// OutputGuard inspects the final assistant text of a text-mode response
// before decoding. Output-tool responses are screened by result guards
// and the decoder instead.
type OutputGuard func(ctx context.Context, content string) Result

// ApplyInput runs the input chain over the prompt and returns the
// prompt to send: the original, or the replacement when a guard
// replaced it. A blocked prompt returns a *Blocked; the caller
// synthesizes the refusal.
func ApplyInput(ctx context.Context, guards []InputGuard, prompt string) (string, *Blocked) {
	for _, guard := range guards {
		verdict := guard(ctx, prompt)
		switch verdict.Action {
		case ActionAllow:
		case ActionReplace:
			prompt = verdict.Text
		case ActionBlock:
			return prompt, &Blocked{Reason: verdict.Reason}
		default:
			return prompt, &Blocked{Reason: fmt.Sprintf("invalid input guard verdict: action %d blocks (fail-closed)", verdict.Action)}
		}
	}
	return prompt, nil
}

// ApplyToolCall runs the tool-argument chain over one call and returns
// the call to execute — possibly with replaced arguments. A nil error
// means run it; a *model.ModelRetry is the correctable rejection the
// retry budget governs; a *Blocked is the refusal the model reads.
func ApplyToolCall(ctx context.Context, guards []ToolGuard, call model.ToolCall) (model.ToolCall, error) {
	for _, guard := range guards {
		verdict := guard(ctx, call)
		switch verdict.Action {
		case ActionAllow:
		case ActionReplace:
			call.Args = json.RawMessage(verdict.Text)
		case ActionBlock:
			return call, &Blocked{Reason: verdict.Reason}
		case ActionRetry:
			return call, &model.ModelRetry{Err: errors.New(verdict.Reason)}
		default:
			return call, &Blocked{Reason: fmt.Sprintf("invalid tool guard verdict: action %d blocks (fail-closed)", verdict.Action)}
		}
	}
	return call, nil
}

// blockedToolResult is the synthetic tool result a block produces: the
// model reads where it expected the tool's output and can route
// around the refusal.
const blockedToolResult = "Your tool call was blocked by policy"

// Refusal is the synthetic tool result text a guard block produces:
// the model reads it where it expected the tool's output and can route
// around the refusal. The runner and guards share one wording so
// evidence reads consistently.
func Refusal(reason string) string {
	if reason == "" {
		reason = "no reason given"
	}
	return blockedToolResult + ": " + reason + "."
}

// ApplyToolResult runs the result chain over one successful tool
// result and returns the result the model sees — the original, a
// replacement, or the block refusal. The *Blocked, when non-nil,
// reports that a guard stopped the value; its Reason is already part
// of the returned result.
func ApplyToolResult(ctx context.Context, guards []ResultGuard, call model.ToolCall, result tool.Result) (tool.Result, *Blocked) {
	for _, guard := range guards {
		verdict := guard(ctx, call, result)
		switch verdict.Action {
		case ActionAllow:
		case ActionReplace:
			result.Text = verdict.Text
		case ActionBlock:
			return tool.Text(Refusal(verdict.Reason)), &Blocked{Reason: verdict.Reason}
		default:
			return tool.Text(blockedToolResult + ": invalid result guard verdict (fail-closed)."), &Blocked{Reason: "invalid result guard verdict"}
		}
	}
	return result, nil
}

// ApplyOutput runs the output chain over the final assistant text and
// returns the text to decode. A blocked output returns a *Blocked;
// the caller fails the run at the policy stage.
func ApplyOutput(ctx context.Context, guards []OutputGuard, content string) (string, *Blocked) {
	for _, guard := range guards {
		verdict := guard(ctx, content)
		switch verdict.Action {
		case ActionAllow:
		case ActionReplace:
			content = verdict.Text
		case ActionBlock:
			return content, &Blocked{Reason: verdict.Reason}
		default:
			return content, &Blocked{Reason: fmt.Sprintf("invalid output guard verdict: action %d blocks (fail-closed)", verdict.Action)}
		}
	}
	return content, nil
}
