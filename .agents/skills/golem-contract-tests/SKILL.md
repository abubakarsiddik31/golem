---
name: golem-contract-tests
description: Add or review Golem Go tests for agent runs, model adapters, tool execution, decoding, error handling, retries, and cancellation. Use whenever behavior changes or a public contract is introduced.
---

# Golem contract tests

Use deterministic fakes to test framework behavior without a network or provider credential. Read `references/test-matrix.md`, then add the smallest tests that protect the changed contract.

## Workflow

1. Identify the caller-visible contract: request, result evidence, context propagation, error identity, and side effects.
2. Write a fake at the Golem-owned port that captures its input and returns controlled output or error.
3. Assert the exact normalized request and ordered result evidence, not implementation-private fields.
4. Test the primary failure stage with `errors.Is` or `errors.As` to prove causal error preservation.
5. Test cancellation propagation for any model, tool, stream, retry, or goroutine behavior.
6. Run `gofmt`, `go test ./...`, and `go vet ./...`.

## Rules

- Never make core unit tests depend on a live model, network, clock, or environment variable.
- Avoid assertions that require sleeps or scheduling luck.
- Keep adapter integration tests in the adapter package and make them opt-in.
- Add a regression test for every fixed bug before or with the fix.

Do not call a test complete merely because it covers lines; it must prove the behavior callers rely on.
