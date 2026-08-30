// Package golem provides typed building blocks for AI agents in Go.
package golem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/abubakarsiddik31/golem/internal/runner"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// DefaultMaxIterations bounds model turns per run when no explicit limit is configured.
const DefaultMaxIterations = 10

// Retry pacing used when attempts are enabled without an explicit backoff
// 500 ms doubling per failed attempt, capped at 30 s.
const (
	retryBaseBackoff = 500 * time.Millisecond
	retryMaxBackoff  = 30 * time.Second
)

// OutputDecoder validates and converts a provider response to the agent's
// declared result type. It is the boundary at which model-produced data becomes
// application data. Returning *model.ModelRetry rejects a response the model
// can correct; with an output retry budget configured, the run feeds the
// rejection back to the model.
type OutputDecoder[Output any] interface {
	Decode(ctx context.Context, response model.Response) (Output, error)
}

// DecodeFunc adapts a function to an OutputDecoder.
type DecodeFunc[Output any] func(context.Context, model.Response) (Output, error)

// Decode converts response using f.
func (f DecodeFunc[Output]) Decode(ctx context.Context, response model.Response) (Output, error) {
	return f(ctx, response)
}

// Agent combines a model, instructions, tools, and a typed output boundary.
// Deps is the dependency value tools receive on every run.
type Agent[Deps any, Output any] struct {
	model             model.Model
	decoder           OutputDecoder[Output]
	instructions      string
	instructionsFunc  func(ctx context.Context, runCtx RunContext[Deps]) string
	historyProcessor  HistoryProcessor
	tools             []tool.Tool[Deps]
	maxIterations     int
	maxAttempts       int
	retryBackoff      func(attempt int) time.Duration
	outputRetries     int
	toolRetries       int
	toolTimeout       time.Duration
	parallelToolCalls bool
	toolChoice        string
	outputSchema      json.RawMessage
	outputToolName    string
	outputToolSpec    model.ToolSpec
	usageLimit        UsageLimit
	runEvents         func(RunEvent)
}

// Option configures an Agent during construction.
type Option[Deps any, Output any] func(*Agent[Deps, Output])

// WithInstructions configures stable system instructions for every run.
func WithInstructions[Deps any, Output any](instructions string) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.instructions = instructions
	}
}

// InstructionsFunc builds the instructions of one run. ctx is the
// caller's run context, so a builder that consults external state can
// honor cancellation.
type InstructionsFunc[Deps any] func(ctx context.Context, runCtx RunContext[Deps]) string

// WithInstructionsFunc configures instructions evaluated at the start of
// every run, so guidance can depend on runtime state such as the run's
// dependency value. The result joins static instructions — static text
// first, separated by a blank line — and an empty result contributes
// nothing. History system messages are replaced by the resolved
// instructions of the current run, exactly as for static instructions.
// Register one function; compose closures when several sources apply.
func WithInstructionsFunc[Deps any, Output any](fn InstructionsFunc[Deps]) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.instructionsFunc = fn
	}
}

// WithTools registers tools the model may request. Tools should be built
// with tool.New; New rejects invalid or duplicate declarations.
func WithTools[Deps any, Output any](tools ...tool.Tool[Deps]) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.tools = append(agent.tools, tools...)
	}
}

// WithMaxIterations bounds model turns per run. It must be at least 1;
// otherwise New fails.
func WithMaxIterations[Deps any, Output any](iterations int) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.maxIterations = iterations
	}
}

// WithMaxAttempts bounds how many times each model call may be attempted,
// including the first, when the model reports a retryable failure (408,
// 429, 5xx, transport faults). Tool and decode failures are never retried.
// The default is 1 — retries are opt-in — and values below 1
// fail New.
func WithMaxAttempts[Deps any, Output any](attempts int) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.maxAttempts = attempts
	}
}

// WithRetryBackoff overrides the wait between retried model calls. backoff
// receives the 1-based number of the attempt that just failed. When
// attempts are enabled without an explicit backoff, runs wait with
// exponential backoff: 500 ms doubling, capped at 30 s.
func WithRetryBackoff[Deps any, Output any](backoff func(attempt int) time.Duration) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.retryBackoff = backoff
	}
}

// WithOutputRetries sets how many correction rounds a decoder may request
// by returning *model.ModelRetry: each round appends the rejection reason
// to the conversation and asks the model again. The default is
// 0 — self-correction is opt-in — and negative values fail New.
func WithOutputRetries[Deps any, Output any](retries int) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.outputRetries = retries
	}
}

// WithOutputSchema declares the JSON Schema document describing the
// agent's expected final answer. Adapters that support structured output
// map it to their native mechanism; adapters that do not ignore it. The
// schema describes the expected shape to the model — the decoder remains
// the validation boundary. An empty schema disables the behavior; a
// non-empty schema that is not valid JSON fails New. Mutually exclusive
// with WithOutputTool, which expresses the same intent through an output
// tool call.
func WithOutputSchema[Deps any, Output any](schema json.RawMessage) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.outputSchema = schema
	}
}

// WithOutputTool declares tool-mode structured output: schema becomes the
// parameters of a synthesized output tool offered to the model, and the
// run ends on the model's first call to it. The call's arguments reach
// the decoder as the final response content, so DecodeJSON validates them
// like any other response — the decoder remains the validation boundary.
//
// Tool mode reaches every model with tool calling, including those
// without native JSON-schema output support. Calls co-emitted with the
// output call are not executed; they are closed with an interrupted
// result so the conversation keeps the call/result pairing providers
// require. The output call itself is closed in the result evidence after
// decoding: a recorded result on success, a rejection bound to the call
// when the decoder asks for correction. Mutually exclusive with
// WithOutputSchema. name must not collide with a registered tool;
// description may be empty; schema must be a non-empty valid JSON
// document.
func WithOutputTool[Deps any, Output any](name, description string, schema json.RawMessage) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.outputToolName = name
		agent.outputToolSpec = model.ToolSpec{Name: name, Description: description, Schema: schema}
	}
}

// WithToolRetries sets how many tool rejections a run feeds back to the
// model: a tool signals correctable arguments by returning an error
// wrapping *model.ModelRetry, and the run delivers the rejection as the
// call's tool result so the model can try again. The default
// is 0 — self-correction is opt-in — and negative values fail New. The
// budget counts total rejections per run and is additionally bounded by
// the model turn limit.
func WithToolRetries[Deps any, Output any](retries int) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.toolRetries = retries
	}
}

// WithToolTimeout sets the default deadline for one tool execution. A tool's
// non-zero Timeout takes precedence. The zero value disables the default;
// negative values fail New. Tools must honor their context so work ends when
// the deadline expires.
func WithToolTimeout[Deps any, Output any](timeout time.Duration) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.toolTimeout = timeout
	}
}

// WithParallelToolCalls lets independent calls returned in one model response
// run concurrently. Result messages remain in model emission order. A tool
// marked Sequential is a barrier: earlier calls finish, it runs alone, then
// later calls begin. The default is false for compatibility and predictable
// side effects.
func WithParallelToolCalls[Deps any, Output any]() Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.parallelToolCalls = true
	}
}

// WithToolChoice restricts this agent's advertised tools to name. It is a
// provider-neutral availability boundary: the selected tool is the only
// function sent to the model, so models that do not support a provider-native
// forced-choice flag still cannot request another registered tool. An empty or
// unregistered name fails New.
func WithToolChoice[Deps any, Output any](name string) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.toolChoice = name
	}
}

// UsageLimit bounds a run's provider-recorded token consumption and its
// model-request and tool-execution activity. The zero value disables the
// limit; each dimension is independent, and zero within a set limit means
// that dimension is unbounded. Providers that do not report usage count
// as zero tokens, so a token limit never trips without provider-reported
// usage; requests and tool executions are counted by the run itself.
type UsageLimit struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	// Requests bounds model calls, retried attempts included.
	Requests int
	// ToolCalls bounds tool executions.
	ToolCalls int
}

// runOptions carries per-run input configuration.
type runOptions struct {
	promptParts []model.Part
}

// RunOption customizes a single run. Options are evaluated once, at run
// start; invalid input fails the run before any model call.
type RunOption func(*runOptions)

// WithPromptParts appends non-text parts, such as images, after the prompt
// text of this run's user message. Parts must be well-formed (see
// model.Part.Validate); a malformed part, or parts on a history message
// other than a user message, fails the run up front.
func WithPromptParts(parts ...model.Part) RunOption {
	return func(opts *runOptions) {
		opts.promptParts = append(opts.promptParts, parts...)
	}
}

// WithPromptImageURL attaches one image reachable at url; the provider
// fetches it. See the multimodal support each adapter documents — not
// every provider accepts image URLs.
func WithPromptImageURL(url string) RunOption {
	return WithPromptParts(model.ImageURL(url))
}

// WithPromptImageData attaches one inline image with its media type, such
// as "image/png". Data is application-owned: treat it as immutable once
// attached.
func WithPromptImageData(mediaType string, data []byte) RunOption {
	return WithPromptParts(model.ImageData(mediaType, data))
}

// validatePromptInput checks the parts this run attaches: every part is
// well-formed, and history carries parts only on user messages. Thinking
// gets the mirror-image rule: reasoning blocks belong to assistant
// messages, so history carrying them elsewhere fails here. Validation
// runs before any model call so invalid input never reaches a provider.
func validatePromptInput(promptParts []model.Part, history []model.Message) error {
	for i, part := range promptParts {
		if err := part.Validate(); err != nil {
			return fmt.Errorf("golem: prompt part %d: %w", i, err)
		}
	}
	for i, message := range history {
		if message.Role != model.RoleUser && len(message.Parts) > 0 {
			return fmt.Errorf("golem: history message %d: parts are only supported on user messages", i)
		}
		for j, part := range message.Parts {
			if err := part.Validate(); err != nil {
				return fmt.Errorf("golem: history message %d part %d: %w", i, j, err)
			}
		}
		if message.Role != model.RoleAssistant && len(message.Thinking) > 0 {
			return fmt.Errorf("golem: history message %d: thinking is only supported on assistant messages", i)
		}
		for j, block := range message.Thinking {
			if err := block.Validate(); err != nil {
				return fmt.Errorf("golem: history message %d thinking block %d: %w", i, j, err)
			}
		}
	}
	return nil
}

// WithUsageLimit bounds the tokens a single run may consume and the model
// requests and tool executions it may make, counted across every model
// turn, retried call, and correction round. The check runs after each
// model response against the run's cumulative usage: the response that
// crosses a bound fails the run at the usage stage, even when it would
// have decoded successfully. Negative values fail New.
func WithUsageLimit[Deps any, Output any](limit UsageLimit) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.usageLimit = limit
	}
}

// New creates an Agent. A model and decoder are both required: Golem never
// guesses how untrusted model output becomes a typed application value.
func New[Deps any, Output any](
	modelClient model.Model,
	decoder OutputDecoder[Output],
	options ...Option[Deps, Output],
) (*Agent[Deps, Output], error) {
	if modelClient == nil {
		return nil, fmt.Errorf("golem: model is required")
	}
	if decoder == nil {
		return nil, fmt.Errorf("golem: output decoder is required")
	}

	agent := &Agent[Deps, Output]{
		model:         modelClient,
		decoder:       decoder,
		maxIterations: DefaultMaxIterations,
		maxAttempts:   1,
	}
	for _, option := range options {
		if option != nil {
			option(agent)
		}
	}
	if agent.maxIterations < 1 {
		return nil, fmt.Errorf("golem: max iterations must be at least 1, got %d", agent.maxIterations)
	}
	if agent.maxAttempts < 1 {
		return nil, fmt.Errorf("golem: max attempts must be at least 1, got %d", agent.maxAttempts)
	}
	if agent.outputRetries < 0 {
		return nil, fmt.Errorf("golem: output retries must not be negative, got %d", agent.outputRetries)
	}
	if agent.toolRetries < 0 {
		return nil, fmt.Errorf("golem: tool retries must not be negative, got %d", agent.toolRetries)
	}
	if agent.toolTimeout < 0 {
		return nil, fmt.Errorf("golem: tool timeout must not be negative, got %s", agent.toolTimeout)
	}
	if len(agent.outputSchema) > 0 && !json.Valid(agent.outputSchema) {
		return nil, fmt.Errorf("golem: output schema is not valid JSON")
	}
	if agent.outputToolName != "" {
		if len(agent.outputSchema) > 0 {
			return nil, fmt.Errorf("golem: output schema and output tool are mutually exclusive; configure one structured-output mode")
		}
		if len(agent.outputToolSpec.Schema) == 0 || !json.Valid(agent.outputToolSpec.Schema) {
			return nil, fmt.Errorf("golem: output tool %q: schema is required and must be valid JSON", agent.outputToolName)
		}
	}
	if agent.usageLimit.InputTokens < 0 || agent.usageLimit.OutputTokens < 0 || agent.usageLimit.TotalTokens < 0 ||
		agent.usageLimit.Requests < 0 || agent.usageLimit.ToolCalls < 0 {
		return nil, fmt.Errorf("golem: usage limit must not be negative, got %+v", agent.usageLimit)
	}
	if err := validateTools(agent.tools); err != nil {
		return nil, err
	}
	if agent.toolChoice != "" {
		if _, ok := findDeclaredTool(agent.tools, agent.toolChoice); !ok {
			return nil, fmt.Errorf("golem: chosen tool %q is not registered", agent.toolChoice)
		}
	}
	if agent.outputToolName != "" {
		for _, t := range agent.tools {
			if t.Name == agent.outputToolName {
				return nil, fmt.Errorf("golem: output tool %q collides with a registered tool", agent.outputToolName)
			}
		}
	}
	return agent, nil
}

func validateTools[Deps any](tools []tool.Tool[Deps]) error {
	seen := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		if t.Name == "" {
			return fmt.Errorf("golem: tool name is required")
		}
		if t.Exec == nil {
			return fmt.Errorf("golem: tool %q: exec function is required", t.Name)
		}
		if t.MaxRetries != nil && *t.MaxRetries < 0 {
			return fmt.Errorf("golem: tool %q: max retries must not be negative, got %d", t.Name, *t.MaxRetries)
		}
		if t.Timeout < 0 {
			return fmt.Errorf("golem: tool %q: timeout must not be negative, got %s", t.Name, t.Timeout)
		}
		if _, duplicate := seen[t.Name]; duplicate {
			return fmt.Errorf("golem: tool %q: duplicate name", t.Name)
		}
		seen[t.Name] = struct{}{}
	}
	return nil
}

// RunContext carries explicit application dependencies for a run. Its Deps
// value flows to every tool executed during the run.
type RunContext[Deps any] struct {
	Deps Deps
}

// Result preserves the typed output and the normalized model evidence that
// produced it, including every tool-call exchange in execution order. This
// makes testing and observability possible without a tracing backend.
//
// A run that pauses on deferred tool calls reports Pending and skips
// decoding: Output is the zero value and Messages ends with the executed
// calls' results only. Check Pending before relying on Output.
type Result[Output any] struct {
	Output   Output
	Messages []model.Message
	Usage    model.Usage
	// Pending is non-nil when the run paused awaiting deferred tool
	// calls; see DeferredRequests for the resolution contract.
	Pending *DeferredRequests
}

// Run executes the agent: it asks the configured model to answer prompt,
// executing requested tools along the way, and decodes the final response.
// Model calls are attempted up to the configured attempt limit; exhausted
// retries fail with the model stage, preserving the provider cause. Output
// the decoder rejects with *model.ModelRetry is fed back for correction up
// to the configured output retry budget, and tool calls a tool rejects
// with *model.ModelRetry are fed back up to the tool retry budget.
//
// Errors are wrapped in RunError with the failing stage. A run that had
// begun producing evidence — completed model turns, reported usage,
// executed tools — carries it as RunError.Partial; a failure before any
// activity leaves Partial nil. Cancellation and deadline errors are
// wrapped like every other failure and remain matchable with errors.Is
// through RunError.Unwrap.
func (a *Agent[Deps, Output]) Run(ctx context.Context, runCtx RunContext[Deps], prompt string, opts ...RunOption) (Result[Output], error) {
	return a.execute(ctx, runCtx, nil, prompt, nil, opts...)
}

// RunWithHistory continues a conversation. history — typically the
// Result.Messages of a previous run — is sent before a fresh user prompt,
// and the result carries the full reconstructed conversation so runs
// chain. The agent's current instructions govern the request: any system
// messages in history are replaced by them, so guidance is re-evaluated
// per run and never duplicated.
//
// History is repaired before the request is built so it keeps the
// call/result pairing providers require: a tool call that never received a
// result — from a crashed or cancelled run, or hand-built history — gets a
// synthesized result stating no outcome was produced, and a result whose
// call is absent is dropped.
func (a *Agent[Deps, Output]) RunWithHistory(ctx context.Context, runCtx RunContext[Deps], history []model.Message, prompt string, opts ...RunOption) (Result[Output], error) {
	return a.execute(ctx, runCtx, history, prompt, nil, opts...)
}

// RunStream executes the agent like Run while streaming progress: every
// model fragment — text, tool-call arguments, and re-streamed correction
// rounds — is forwarded to onDelta in arrival order, across tool turns.
// The returned Result is identical in shape to Run's; deltas
// are advisory progress on top of the canonical run.
//
// The model must implement model.StreamingModel; otherwise RunStream
// fails up front with a plain error, before any stage runs — there is no
// silent fallback to non-streaming generation. Streamed model turns are
// single-attempt: retryable failures fail the run at the model stage
// instead of being retried, because a retried stream would replay
// fragments the caller already saw. An error returned from onDelta stops
// the run and surfaces at the model stage with the original error
// reachable via errors.Is. A nil onDelta is allowed and discards
// fragments. Failures carry RunError.Partial evidence like Run's.
func (a *Agent[Deps, Output]) RunStream(ctx context.Context, runCtx RunContext[Deps], prompt string, onDelta func(model.Delta) error, opts ...RunOption) (Result[Output], error) {
	if _, ok := a.model.(model.StreamingModel); !ok {
		return Result[Output]{}, fmt.Errorf("golem: model %T does not support streaming", a.model)
	}
	return a.execute(ctx, runCtx, nil, prompt, onDelta, opts...)
}

// RunStreamWithHistory continues a conversation like RunWithHistory while
// streaming progress; see RunStream for the streaming contract.
func (a *Agent[Deps, Output]) RunStreamWithHistory(ctx context.Context, runCtx RunContext[Deps], history []model.Message, prompt string, onDelta func(model.Delta) error, opts ...RunOption) (Result[Output], error) {
	if _, ok := a.model.(model.StreamingModel); !ok {
		return Result[Output]{}, fmt.Errorf("golem: model %T does not support streaming", a.model)
	}
	return a.execute(ctx, runCtx, history, prompt, onDelta, opts...)
}

func (a *Agent[Deps, Output]) execute(ctx context.Context, runCtx RunContext[Deps], history []model.Message, prompt string, onDelta func(model.Delta) error, opts ...RunOption) (Result[Output], error) {
	runOpts, history, err := a.prepareRun(ctx, history, opts...)
	if err != nil {
		return Result[Output]{}, err
	}
	messages := a.requestMessages(a.resolveInstructions(ctx, runCtx), history, prompt, runOpts.promptParts)
	return a.runLoop(ctx, runCtx, messages, onDelta)
}

// runLoop drives the shared tail of every run variant: the runner loop
// over the prepared request conversation, the usage bound, and the
// decode-or-correct boundary. It also returns a paused run: an outcome
// carrying pending deferred calls skips decoding — a pause has no final
// answer to validate — and surfaces them on Result.Pending.
func (a *Agent[Deps, Output]) runLoop(ctx context.Context, runCtx RunContext[Deps], messages []model.Message, onDelta func(model.Delta) error) (Result[Output], error) {
	var specs []model.ToolSpec
	for _, t := range a.tools {
		if a.toolChoice != "" && t.Name != a.toolChoice {
			continue
		}
		specs = append(specs, model.ToolSpec{
			Name:        t.Name,
			Description: t.Description,
			Schema:      t.Schema,
		})
	}
	if a.outputToolName != "" {
		specs = append(specs, a.outputToolSpec)
	}
	retry := runner.RetryConfig{MaxAttempts: a.maxAttempts, Backoff: a.retryBackoff}
	if retry.Backoff == nil && a.maxAttempts > 1 {
		retry.Backoff = exponentialBackoff
	}

	var usage model.Usage
	var modelCalls, toolExecutions int

	for attempt := 0; ; attempt++ {
		request := model.Request{Messages: messages, ToolSpecs: specs, OutputSchema: a.outputSchema}
		var outcome runner.Outcome
		var err error
		if onDelta != nil {
			outcome, err = runner.ExecuteStreamWithToolConfig(ctx, a.model, a.tools, runCtx.Deps,
				request, a.maxIterations, runner.ToolConfig{DefaultRetries: a.toolRetries, DefaultTimeout: a.toolTimeout, Parallel: a.parallelToolCalls}, a.outputToolName, a.runEvents, onDelta)
		} else {
			outcome, err = runner.ExecuteWithToolConfig(ctx, a.model, a.tools, runCtx.Deps,
				request, a.maxIterations, retry, runner.ToolConfig{DefaultRetries: a.toolRetries, DefaultTimeout: a.toolTimeout, Parallel: a.parallelToolCalls}, a.outputToolName, a.runEvents)
		}
		if err != nil {
			usage.InputTokens += outcome.Usage.InputTokens
			usage.OutputTokens += outcome.Usage.OutputTokens
			modelCalls += outcome.ModelCalls
			toolExecutions += outcome.ToolExecutions
			return Result[Output]{}, classifyRunError(err, partialEvidence(outcome.Messages, usage, modelCalls, toolExecutions))
		}
		usage.InputTokens += outcome.Usage.InputTokens
		usage.OutputTokens += outcome.Usage.OutputTokens
		modelCalls += outcome.ModelCalls
		toolExecutions += outcome.ToolExecutions
		if err := a.usageLimit.check(usage, modelCalls, toolExecutions); err != nil {
			return Result[Output]{}, &RunError{Stage: StageUsage, Err: err,
				Partial: partialEvidence(outcome.Messages, usage, modelCalls, toolExecutions)}
		}
		if len(outcome.Pending) > 0 {
			return Result[Output]{Messages: outcome.Messages, Usage: usage, Pending: deferredRequests(outcome.Pending)}, nil
		}

		output, err := a.decoder.Decode(ctx, outcome.Response)
		if err == nil {
			return Result[Output]{Output: output, Messages: a.closeOutputCall(outcome.Messages, outcome.Response), Usage: usage}, nil
		}
		var rejection *model.ModelRetry
		if !errors.As(err, &rejection) || attempt >= a.outputRetries {
			if attempt > 0 {
				err = fmt.Errorf("golem: output failed validation after %d attempts: %w", attempt+1, err)
			}
			return Result[Output]{}, &RunError{Stage: StageDecode, Err: err,
				Partial: partialEvidence(outcome.Messages, usage, modelCalls, toolExecutions)}
		}
		if a.runEvents != nil {
			a.runEvents(RunEvent{Kind: EventOutputRejected, Attempt: attempt + 1, Err: rejection})
		}
		// Correction round: keep the rejected response as
		// evidence, tell the model why it was rejected, and run again.
		// In tool mode the rejection binds to the output call, so the
		// correction round keeps the pairing providers require.
		if call, ok := findOutputCall(outcome.Response.Message.ToolCalls, a.outputToolName); ok {
			messages = append(outcome.Messages, model.Message{
				Role:       model.RoleTool,
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Content: fmt.Sprintf("Your result was rejected: %v. "+
					"Correct the arguments and call %s again.", rejection.Err, a.outputToolName),
			})
			continue
		}
		messages = append(outcome.Messages, model.Message{
			Role:    model.RoleUser,
			Content: fmt.Sprintf("Your previous response was rejected: %v. Correct it and respond again.", rejection.Err),
		})
	}
}

func findDeclaredTool[Deps any](tools []tool.Tool[Deps], name string) (tool.Tool[Deps], bool) {
	for _, t := range tools {
		if t.Name == name {
			return t, true
		}
	}
	return tool.Tool[Deps]{}, false
}

// recordedToolResult closes a successfully decoded output-tool call in the
// result evidence: the run completed with this output.
const recordedToolResult = "Result recorded."

// closeOutputCall closes the output-tool call of the final response, when
// the run used one, so result evidence keeps the call/result pairing
// providers require. On success the close is a recorded result; for a
// correction round the caller appends the rejection itself, bound to the
// call.
func (a *Agent[Deps, Output]) closeOutputCall(evidence []model.Message, response model.Response) []model.Message {
	if a.outputToolName == "" {
		return evidence
	}
	call, ok := findOutputCall(response.Message.ToolCalls, a.outputToolName)
	if !ok {
		return evidence
	}
	return append(evidence, model.Message{
		Role:       model.RoleTool,
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    recordedToolResult,
	})
}

func findOutputCall(calls []model.ToolCall, name string) (model.ToolCall, bool) {
	for _, call := range calls {
		if call.Name == name {
			return call, true
		}
	}
	return model.ToolCall{}, false
}

// resolveInstructions builds the instructions governing one run: the
// static text, then the evaluated function's result, separated by a blank
// line. Evaluation happens once per run, before the request conversation
// is built.
func (a *Agent[Deps, Output]) resolveInstructions(ctx context.Context, runCtx RunContext[Deps]) string {
	instructions := a.instructions
	if a.instructionsFunc == nil {
		return instructions
	}
	dynamic := a.instructionsFunc(ctx, runCtx)
	switch {
	case dynamic == "":
		return instructions
	case instructions == "":
		return dynamic
	default:
		return instructions + "\n\n" + dynamic
	}
}

// requestMessages builds the ordered request conversation: the run's
// resolved instructions when set, the repaired history with system messages
// removed (instructions govern every run), and the new user prompt with
// its attached parts.
func (a *Agent[Deps, Output]) requestMessages(instructions string, history []model.Message, prompt string, promptParts []model.Part) []model.Message {
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
	return append(messages, model.Message{Role: model.RoleUser, Content: prompt, Parts: promptParts})
}

// exponentialBackoff paces enabled retries: 500 ms after the first failed
// attempt, doubling per attempt, capped at 30 s.
func exponentialBackoff(attempt int) time.Duration {
	if attempt < 1 {
		return retryBaseBackoff
	}
	delay := retryBaseBackoff << (attempt - 1)
	if delay <= 0 || delay > retryMaxBackoff {
		return retryMaxBackoff
	}
	return delay
}

// classifyRunError maps runner outcomes to public stages, attaching the
// run's partial evidence. Cancellation and deadline errors ride a RunError
// like every other failure — Unwrap keeps them matchable with errors.Is —
// because wrapping is the only way their evidence survives.
func classifyRunError(err error, partial *PartialResult) error {
	var toolErr *runner.ToolError
	switch {
	case errors.As(err, &toolErr):
		return &RunError{Stage: StageTool, Err: err, Partial: partial}
	case errors.Is(err, runner.ErrLoopLimit):
		return &RunError{Stage: StageLoop, Err: err, Partial: partial}
	default:
		return &RunError{Stage: StageModel, Err: err, Partial: partial}
	}
}

// partialEvidence builds the failed run's evidence record, or nil when
// the run produced nothing worth preserving: no model turn completed,
// no usage was reported, and no tool executed — a lone failed provider
// attempt is not evidence.
func partialEvidence(messages []model.Message, usage model.Usage, requests, toolCalls int) *PartialResult {
	if toolCalls == 0 && usage.InputTokens == 0 && usage.OutputTokens == 0 && !hasAssistantTurn(messages) {
		return nil
	}
	return &PartialResult{Messages: messages, Usage: usage, Requests: requests, ToolCalls: toolCalls}
}

// hasAssistantTurn reports whether any completed model turn is in the
// evidence.
func hasAssistantTurn(messages []model.Message) bool {
	for _, message := range messages {
		if message.Role == model.RoleAssistant {
			return true
		}
	}
	return false
}
