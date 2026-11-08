# Guardrails

## Purpose

Run your checks at a run's boundaries: the prompt before the model
sees it, each tool call's arguments before the tool runs, each tool
result before the model reads it, and the final answer before the
caller gets it. Use them to redact secrets, refuse off-topic prompts
cheaply, keep untrusted tool output away from the model, or stop an
answer that would expose personal data.

## When to use

Whenever content crosses a trust boundary inside a run: user input
into the model, external content out of tools, or model output out of
the run. When the application already controls every value — it builds
the prompt and owns the tools — plain code at the call site is simpler.
Guards complement, not replace, the static defenses: the shell command
policy (see command-execution), the web-fetch address guard, and
OS-level isolation remain the boundary for what a command may touch.

## How it works

Four option functions register chains of guards; each chain runs in
registration order at its boundary. A guard returns a `guard.Result`:

| Verdict | Input (prompt) | Tool call (arguments) | Tool result | Output (answer) |
|---|---|---|---|---|
| Allow | next guard | next guard | next guard | next guard |
| Replace | send the replacement | execute with the replacement JSON | model reads the replacement | decode the replacement |
| Block | no model call; a synthesized refusal is decoded as the output | no execution; the model reads a synthetic refusal | model reads a synthetic refusal | run fails: `RunError{Stage: StagePolicy}` wrapping `*guard.Blocked` |
| Retry | invalid — blocks (fail-closed) | rejection rides the `WithToolRetries` budget | invalid — blocks (fail-closed) | invalid — blocks (fail-closed) |

A `Replace` threads forward: the guards after it see the replacement,
which is what makes a redactor-first chain clean everything after it.
The chain application is `guard.ApplyInput`, `guard.ApplyToolCall`,
`guard.ApplyToolResult`, and `guard.ApplyOutput` — deterministic,
unit-tested functions the run calls; the run adds only wiring.

Boundary specifics:

- **Input.** Guards run on every run's prompt text, including runs
  that resume a history. The supplied history itself is trusted and
  unscreened — for history from an untrusted client, run
  `golem.SanitizeHistory` first. A blocked prompt ends the run without
  a model call: the original prompt stays in the conversation as
  evidence and the refusal rides the normal decode path, so a decoder
  that cannot accept plain refusal text (a strict JSON decoder, for
  instance) fails the run at the decode stage.
- **Tool calls.** A blocked or rejected call never executes and does
  not count toward the run's tool-call activity, but it emits its
  start and end events like any call. A `Replace` swaps the call's
  arguments with the guard's text as raw JSON; malformed replacements
  surface as the tool's own correctable rejection. A `Retry` is the
  guard's version of a tool rejection: the reason becomes the call's
  feedback and the model can correct within the `golem.WithToolRetries`
  budget (zero by default — an unbudgeted run treats the first retry
  verdict as terminal).
- **Tool results.** The tool has already run when a result guard
  fires — blocking its result does not undo its side effects. Failed
  tools (the `tool.Failed` sentinel) and model rejections keep their
  existing semantics; guards screen successful results.
- **Output.** Output guards screen text-mode responses. Output-tool
  responses are screened by result guards and the decoder instead; a
  blocked output fails the run — the model already produced it, so the
  caller decides what happens next, with the transcript preserved in
  `RunError.Partial`.

## Example

Run `examples/guardrails` — offline: a scripted fake model drives a
redacted prompt, a blocked injected tool result, and a blocked answer.

```go
agent, err := golem.New[struct{}, string](client, decoder,
	golem.WithInputGuards[struct{}, string](redactSecret),
	golem.WithResultGuards[struct{}, string](blockInjectedContent),
	golem.WithOutputGuards[struct{}, string](noPII),
)
```

## API surface

- `guard.InputGuard`, `guard.ToolGuard`, `guard.ResultGuard`, `guard.OutputGuard` — the guard function types
- `guard.Result`, `guard.Action` (`ActionAllow`/`ActionBlock`/`ActionReplace`/`ActionRetry`), `guard.Allow/Block/Replace/Retry` — the verdict
- `guard.Blocked` — the error a block produces; `errors.As` through `RunError` at the policy stage
- `guard.ApplyInput`, `guard.ApplyToolCall`, `guard.ApplyToolResult`, `guard.ApplyOutput` — the chain application, exported for testing and reuse
- `guard.Refusal(reason string) string` — the synthetic refusal wording
- `golem.WithInputGuards`, `golem.WithToolGuards`, `golem.WithResultGuards`, `golem.WithOutputGuards` — the registration options

## Gotchas

- Guards are guardrails against accidents, not a security boundary:
  blocks are graceful, the model sees the refusal text, and a model
  that wants to work around a guard can try. Isolation stays the
  application's job.
- An invalid verdict at a boundary blocks (fail-closed) with a reason
  naming the misuse — a `Retry` returned from an input guard, say —
  rather than silently passing the value through.
- Guards are plain closures over application state; they do not see
  the run's typed dependencies (ADR 0042). Capture what you need when
  constructing the agent.
- The decode-stage failure of a blocked prompt under a strict decoder
  is deliberate: a refusal is text, and a decoder that only accepts
  your output schema should refuse it. Use a decoder that handles both
  when you register input guards.
- Deciding ADR: `docs/adr/0042-guardrails.md`.
