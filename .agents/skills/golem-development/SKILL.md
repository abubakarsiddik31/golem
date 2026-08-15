---
name: golem-development
description: Build or revise the Golem Go AI-agent framework. Use when changing Go APIs, model adapters, tool execution, structured output, retries, tracing, tests, or documentation in this repository.
---

# Golem development

Build a small, Go-native agent framework with explicit contracts. Read `docs/foundation.md` first. Read `references/patterns.md` before designing a public API or an execution-loop change.

## Workflow

1. **Orient.** Identify the affected public package, package boundary, and existing contract tests. Check `AGENTS.md` for non-negotiable rules.
2. **State the contract.** For new behavior, define inputs, output, cancellation, error classification, and observable evidence before adding implementation.
3. **Choose the smallest fit.** Prefer a concrete type or option function. Add an interface only where callers must substitute an implementation.
4. **Implement at a boundary.** Keep provider SDK types in adapters; validate model-produced data before it becomes an application value.
5. **Verify.** Format changed Go files, run `go test ./...` and `go vet ./...`. Cover both a successful fake-model path and a failure/cancellation path when applicable.
6. **Document public intent.** Update the README for user-facing APIs. Write an ADR under `docs/adr/` for a difficult-to-reverse cross-package decision.

## Required design checks

- Put `context.Context` first on blocking, model, and tool methods.
- Carry dependencies through a typed run context. Never reach for global mutable state or `map[string]any` as an escape hatch.
- Keep orchestration deterministic: append message evidence in execution order and make retry policy explicit.
- Preserve source errors with wrapping so callers can use `errors.Is` and `errors.As`.
- Do not create goroutines unless ownership, cancellation, completion, and error delivery are each specified and tested.
- Do not import a provider SDK from `golem` core packages.

## Review checklist

Before committing, ask:

- Does this make the usual agent path shorter without hiding significant behavior?
- Is the public API provider-neutral and necessary now?
- Can a fake model or fake tool test it with no network access?
- Does `Result` retain enough evidence to debug the outcome?
- Is an error actionable and classifiable at the caller boundary?

Use `references/patterns.md` for preferred examples and anti-patterns.
