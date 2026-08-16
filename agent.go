// Package golem provides typed building blocks for AI agents in Go.
package golem

import (
	"context"
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
	model         model.Model
	decoder       OutputDecoder[Output]
	instructions  string
	tools         []tool.Tool[Deps]
	maxIterations int
	maxAttempts   int
	retryBackoff  func(attempt int) time.Duration
	outputRetries int
	toolRetries   int
}

// Option configures an Agent during construction.
type Option[Deps any, Output any] func(*Agent[Deps, Output])

// WithInstructions configures stable system instructions for every run.
func WithInstructions[Deps any, Output any](instructions string) Option[Deps, Output] {
	return func(agent *Agent[Deps, Output]) {
		agent.instructions = instructions
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
	if err := validateTools(agent.tools); err != nil {
		return nil, err
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
type Result[Output any] struct {
	Output   Output
	Messages []model.Message
	Usage    model.Usage
}

// Run executes the agent: it asks the configured model to answer prompt,
// executing requested tools along the way, and decodes the final response.
// Model calls are attempted up to the configured attempt limit; exhausted
// retries fail with the model stage, preserving the provider cause. Output
// the decoder rejects with *model.ModelRetry is fed back for correction up
// to the configured output retry budget, and tool calls a tool rejects
// with *model.ModelRetry are fed back up to the tool retry budget.
//
// Errors are wrapped in RunError with the failing stage. Cancellation and
// deadline errors are returned unwrapped so callers can match them
// directly with errors.Is.
func (a *Agent[Deps, Output]) Run(ctx context.Context, runCtx RunContext[Deps], prompt string) (Result[Output], error) {
	return a.execute(ctx, runCtx, nil, prompt, nil)
}

// RunWithHistory continues a conversation. history — typically the
// Result.Messages of a previous run — is sent before a fresh user prompt,
// and the result carries the full reconstructed conversation so runs
// chain. The agent's current instructions govern the request: any
// system messages in history are replaced by them, so guidance is
// re-evaluated per run and never duplicated.
func (a *Agent[Deps, Output]) RunWithHistory(ctx context.Context, runCtx RunContext[Deps], history []model.Message, prompt string) (Result[Output], error) {
	return a.execute(ctx, runCtx, history, prompt, nil)
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
// fragments.
func (a *Agent[Deps, Output]) RunStream(ctx context.Context, runCtx RunContext[Deps], prompt string, onDelta func(model.Delta) error) (Result[Output], error) {
	if _, ok := a.model.(model.StreamingModel); !ok {
		return Result[Output]{}, fmt.Errorf("golem: model %T does not support streaming", a.model)
	}
	return a.execute(ctx, runCtx, nil, prompt, onDelta)
}

// RunStreamWithHistory continues a conversation like RunWithHistory while
// streaming progress; see RunStream for the streaming contract.
func (a *Agent[Deps, Output]) RunStreamWithHistory(ctx context.Context, runCtx RunContext[Deps], history []model.Message, prompt string, onDelta func(model.Delta) error) (Result[Output], error) {
	if _, ok := a.model.(model.StreamingModel); !ok {
		return Result[Output]{}, fmt.Errorf("golem: model %T does not support streaming", a.model)
	}
	return a.execute(ctx, runCtx, history, prompt, onDelta)
}

func (a *Agent[Deps, Output]) execute(ctx context.Context, runCtx RunContext[Deps], history []model.Message, prompt string, onDelta func(model.Delta) error) (Result[Output], error) {
	var specs []model.ToolSpec
	for _, t := range a.tools {
		specs = append(specs, model.ToolSpec{
			Name:        t.Name,
			Description: t.Description,
			Schema:      t.Schema,
		})
	}
	retry := runner.RetryConfig{MaxAttempts: a.maxAttempts, Backoff: a.retryBackoff}
	if retry.Backoff == nil && a.maxAttempts > 1 {
		retry.Backoff = exponentialBackoff
	}

	messages := a.requestMessages(history, prompt)
	var usage model.Usage

	for attempt := 0; ; attempt++ {
		request := model.Request{Messages: messages, ToolSpecs: specs}
		var outcome runner.Outcome
		var err error
		if onDelta != nil {
			outcome, err = runner.ExecuteStream(ctx, a.model, a.tools, runCtx.Deps,
				request, a.maxIterations, a.toolRetries, onDelta)
		} else {
			outcome, err = runner.Execute(ctx, a.model, a.tools, runCtx.Deps,
				request, a.maxIterations, retry, a.toolRetries)
		}
		if err != nil {
			return Result[Output]{}, classifyRunError(err)
		}
		usage.InputTokens += outcome.Usage.InputTokens
		usage.OutputTokens += outcome.Usage.OutputTokens

		output, err := a.decoder.Decode(ctx, outcome.Response)
		if err == nil {
			return Result[Output]{Output: output, Messages: outcome.Messages, Usage: usage}, nil
		}
		var rejection *model.ModelRetry
		if !errors.As(err, &rejection) || attempt >= a.outputRetries {
			if attempt > 0 {
				err = fmt.Errorf("golem: output failed validation after %d attempts: %w", attempt+1, err)
			}
			return Result[Output]{}, &RunError{Stage: StageDecode, Err: err}
		}
		// Correction round: keep the rejected response as
		// evidence, tell the model why it was rejected, and run again.
		messages = append(outcome.Messages, model.Message{
			Role:    model.RoleUser,
			Content: fmt.Sprintf("Your previous response was rejected: %v. Correct it and respond again.", rejection.Err),
		})
	}
}

// requestMessages builds the ordered request conversation: the agent's
// current instructions when set, the supplied history with system messages
// removed (instructions govern every run), and the new user
// prompt.
func (a *Agent[Deps, Output]) requestMessages(history []model.Message, prompt string) []model.Message {
	messages := make([]model.Message, 0, len(history)+2)
	if a.instructions != "" {
		messages = append(messages, model.Message{
			Role:    model.RoleSystem,
			Content: a.instructions,
		})
	}
	for _, message := range history {
		if message.Role == model.RoleSystem {
			continue
		}
		messages = append(messages, message)
	}
	return append(messages, model.Message{Role: model.RoleUser, Content: prompt})
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

// classifyRunError maps runner outcomes to public stages. Cancellation and
// deadline errors stay unwrapped so callers can match them with errors.Is.
func classifyRunError(err error) error {
	var toolErr *runner.ToolError
	switch {
	case errors.As(err, &toolErr):
		return &RunError{Stage: StageTool, Err: err}
	case errors.Is(err, runner.ErrLoopLimit):
		return &RunError{Stage: StageLoop, Err: err}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return &RunError{Stage: StageModel, Err: err}
	}
}
