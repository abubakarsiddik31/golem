package golem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/abubakarsiddik31/golem/internal/runner"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// DeferredRequests enumerates the tool calls that paused a run, grouped
// by what resolving them requires. Approvals wait for a human decision;
// External wait for a result produced outside the run.
type DeferredRequests struct {
	Approvals []PendingToolCall
	External  []PendingToolCall
}

// PendingToolCall is one deferred call awaiting resolution.
type PendingToolCall struct {
	// CallID identifies the call; resolution results are keyed by it.
	CallID string
	// ToolName and Args identify what the model asked for. Args is
	// model-produced JSON — validate before trusting it.
	ToolName string
	Args     json.RawMessage
	// Reason is the tool's explanation to whoever resolves the request,
	// such as the approval prompt text or a correlation key.
	Reason string
}

// Approval is the decision on one deferred approval request, keyed by
// call ID in DeferredResults.Approvals.
type Approval struct {
	// Approved re-executes the tool with the approved marker set, so the
	// gated action happens inside the run that holds the approval.
	Approved bool
	// Reason is shown to the model when Approved is false; empty falls
	// back to a plain denial message. It is ignored on approval.
	Reason string
}

// DeferredResults resolves the pending calls of a paused run, keyed by
// call ID. Every pending call must be resolved exactly once and no
// unknown call ID is accepted; validation fails the resume run before
// any model call.
type DeferredResults struct {
	// Approvals carries the human decision per approval request.
	Approvals map[string]Approval
	// External carries the result per externally executed call, handed
	// to the model verbatim as the call's tool result.
	External map[string]string
}

// RunWithDeferredResults resumes a run that paused on deferred tool
// calls. history is the paused run's Result.Messages; results resolves
// every pending call; prompt optionally continues the conversation with
// a new user message — an empty prompt resumes on the resolutions alone.
//
// Approved calls re-execute their tool with the approved marker set (see
// tool.CallApproved) under the configured tool timeout; a re-run that
// fails — or defers again — fails the resume run at the tool stage.
// Denied calls and external results become the calls' tool results, in
// the model's emission order. The resumed run continues through the
// ordinary loop and may itself pause again.
func (a *Agent[Deps, Output]) RunWithDeferredResults(ctx context.Context, runCtx RunContext[Deps], history []model.Message, results DeferredResults, prompt string, opts ...RunOption) (Result[Output], error) {
	runOpts, history, err := a.prepareRun(ctx, history, opts...)
	if err != nil {
		return Result[Output]{}, err
	}
	pending, err := pendingDeferredCalls(history)
	if err != nil {
		return Result[Output]{}, err
	}
	if err := validateDeferredResults(pending, results); err != nil {
		return Result[Output]{}, err
	}
	resolutions, err := a.resolvePending(ctx, runCtx, pending, results)
	if err != nil {
		return Result[Output]{}, err
	}
	messages := a.resumeMessages(a.resolveInstructions(ctx, runCtx),
		mergeResolutions(history, resolutions), prompt, runOpts.promptParts)
	return a.runLoop(ctx, runCtx, messages, nil)
}

// prepareRun applies run options and the history processor, then
// validates the prompt input: the shared preamble of run variants that
// start from caller-supplied history.
func (a *Agent[Deps, Output]) prepareRun(ctx context.Context, history []model.Message, opts ...RunOption) (runOptions, []model.Message, error) {
	var runOpts runOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&runOpts)
		}
	}
	if a.historyProcessor != nil {
		processed, err := a.historyProcessor(ctx, history)
		if err != nil {
			return runOpts, nil, fmt.Errorf("golem: history processor: %w", err)
		}
		if processed == nil {
			processed = []model.Message{}
		}
		history = processed
	}
	if err := validatePromptInput(runOpts.promptParts, history); err != nil {
		return runOpts, nil, err
	}
	return runOpts, history, nil
}

// deferredRequests maps runner pending calls to the public grouping.
func deferredRequests(pending []runner.PendingCall) *DeferredRequests {
	requests := &DeferredRequests{}
	for _, item := range pending {
		call := PendingToolCall{
			CallID:   item.Call.ID,
			ToolName: item.Call.Name,
			Args:     item.Call.Args,
			Reason:   item.Defer.Reason,
		}
		if item.Defer.Kind == tool.DeferApproval {
			requests.Approvals = append(requests.Approvals, call)
		} else {
			requests.External = append(requests.External, call)
		}
	}
	return requests
}

// pendingDeferredCalls returns the calls of the history that no tool
// result answers, in emission order — the calls a paused run awaits.
func pendingDeferredCalls(history []model.Message) ([]model.ToolCall, error) {
	answered := make(map[string]struct{})
	var calls []model.ToolCall
	for _, message := range history {
		switch message.Role {
		case model.RoleAssistant:
			calls = append(calls, message.ToolCalls...)
		case model.RoleTool:
			if message.ToolCallID != "" {
				answered[message.ToolCallID] = struct{}{}
			}
		}
	}
	var pending []model.ToolCall
	for _, call := range calls {
		if call.ID == "" {
			return nil, fmt.Errorf("golem: deferred tool call %q has no call ID and cannot be resolved", call.Name)
		}
		if _, done := answered[call.ID]; !done {
			pending = append(pending, call)
		}
	}
	if len(pending) == 0 {
		return nil, fmt.Errorf("golem: history has no pending deferred tool calls to resolve")
	}
	return pending, nil
}

// validateDeferredResults requires every pending call to be resolved in
// exactly one map, and every resolution to name a pending call.
func validateDeferredResults(pending []model.ToolCall, results DeferredResults) error {
	known := make(map[string]struct{}, len(pending))
	for _, call := range pending {
		known[call.ID] = struct{}{}
	}
	for id := range results.Approvals {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("golem: approval for unknown tool call %q", id)
		}
		if _, also := results.External[id]; also {
			return fmt.Errorf("golem: tool call %q is resolved as both an approval and an external result", id)
		}
	}
	for id := range results.External {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("golem: external result for unknown tool call %q", id)
		}
	}
	for _, call := range pending {
		if _, approved := results.Approvals[call.ID]; approved {
			continue
		}
		if _, external := results.External[call.ID]; external {
			continue
		}
		return fmt.Errorf("golem: pending tool call %q (%s) has no resolution", call.ID, call.Name)
	}
	return nil
}

// resolvePending turns each resolution into its tool-result message:
// an approved call re-executes through the runtime's approved path, a
// denial becomes a message the model cannot mistake for a result, and
// an external result is carried verbatim.
func (a *Agent[Deps, Output]) resolvePending(ctx context.Context, runCtx RunContext[Deps], pending []model.ToolCall, results DeferredResults) (map[string]model.Message, error) {
	resolutions := make(map[string]model.Message, len(pending))
	for _, call := range pending {
		if decision, ok := results.Approvals[call.ID]; ok {
			if !decision.Approved {
				resolutions[call.ID] = model.Message{
					Role: model.RoleTool, ToolCallID: call.ID, ToolName: call.Name,
					Content: deniedToolResult(decision.Reason),
				}
				continue
			}
			declared, ok := findDeclaredTool(a.tools, call.Name)
			if !ok {
				return nil, fmt.Errorf("golem: pending tool call %q: tool %q is not registered", call.ID, call.Name)
			}
			result, err := runner.ExecuteApprovedTool(ctx, declared, runCtx.Deps, call.Args, a.toolTimeout)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return nil, err
				}
				return nil, classifyRunError(&runner.ToolError{ToolName: call.Name, CallID: call.ID,
					Err: fmt.Errorf("approved re-run failed: %w", err)})
			}
			resolutions[call.ID] = model.Message{
				Role: model.RoleTool, ToolCallID: call.ID, ToolName: call.Name, Content: result,
			}
			continue
		}
		resolutions[call.ID] = model.Message{
			Role: model.RoleTool, ToolCallID: call.ID, ToolName: call.Name,
			Content: results.External[call.ID],
		}
	}
	return resolutions, nil
}

// deniedToolResult is the tool result a denied approval produces. It
// states the decision and its consequence so the model neither retries
// the call nor treats the denial as a provider failure.
func deniedToolResult(reason string) string {
	if reason == "" {
		return "Your tool call was denied. Do not call it again; continue without its result."
	}
	return fmt.Sprintf("Your tool call was denied: %s. Do not call it again; continue without its result.", reason)
}

// mergeResolutions splices resolution messages into the paused evidence.
// The final assistant turn's calls get their results in emission order —
// existing executed results and new resolutions interleaved — and any
// resolution for a call from an older turn appends after the batch,
// where history repair still sees valid call/result pairing.
func mergeResolutions(history []model.Message, resolutions map[string]model.Message) []model.Message {
	last := -1
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == model.RoleAssistant && len(history[i].ToolCalls) > 0 {
			last = i
			break
		}
	}
	if last < 0 {
		merged := make([]model.Message, 0, len(history)+len(resolutions))
		merged = append(merged, history...)
		for _, call := range orderedResolutions(resolutions) {
			merged = append(merged, call)
		}
		return merged
	}

	existing := make(map[string]model.Message)
	for _, message := range history[last+1:] {
		if message.Role == model.RoleTool {
			existing[message.ToolCallID] = message
		}
	}
	merged := make([]model.Message, 0, len(history)+len(resolutions))
	merged = append(merged, history[:last+1]...)
	consumed := make(map[string]struct{}, len(resolutions))
	for _, call := range history[last].ToolCalls {
		if resolution, ok := resolutions[call.ID]; ok {
			merged = append(merged, resolution)
			consumed[call.ID] = struct{}{}
			continue
		}
		if result, ok := existing[call.ID]; ok {
			merged = append(merged, result)
			consumed[call.ID] = struct{}{}
		}
	}
	for _, message := range history[last+1:] {
		if _, used := consumed[message.ToolCallID]; !used && message.Role == model.RoleTool {
			merged = append(merged, message)
		}
	}
	for _, resolution := range orderedResolutions(resolutions) {
		if _, used := consumed[resolution.ToolCallID]; !used {
			merged = append(merged, resolution)
		}
	}
	return merged
}

// orderedResolutions returns the resolutions in map-independent order for
// the fallback paths that append them.
func orderedResolutions(resolutions map[string]model.Message) []model.Message {
	ordered := make([]model.Message, 0, len(resolutions))
	for _, id := range sortedKeys(resolutions) {
		ordered = append(ordered, resolutions[id])
	}
	return ordered
}

func sortedKeys(resolutions map[string]model.Message) []string {
	keys := make([]string, 0, len(resolutions))
	for id := range resolutions {
		keys = append(keys, id)
	}
	slices.Sort(keys)
	return keys
}

// resumeMessages builds the resumed request conversation: the run's
// resolved instructions, the repaired history — now fully paired — and
// the optional new user prompt. An empty prompt with no parts adds no
// user message: the resolutions alone re-open the conversation.
func (a *Agent[Deps, Output]) resumeMessages(instructions string, history []model.Message, prompt string, promptParts []model.Part) []model.Message {
	repaired := runner.RepairHistory(history)
	messages := make([]model.Message, 0, len(repaired)+2)
	if instructions != "" {
		messages = append(messages, model.Message{
			Role:    model.RoleSystem,
			Content: instructions,
		})
	}
	for _, message := range repaired {
		if message.Role == model.RoleSystem {
			continue
		}
		messages = append(messages, message)
	}
	if prompt != "" || len(promptParts) > 0 {
		messages = append(messages, model.Message{Role: model.RoleUser, Content: prompt, Parts: promptParts})
	}
	return messages
}
