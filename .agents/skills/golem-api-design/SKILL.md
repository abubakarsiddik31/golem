---
name: golem-api-design
description: Design or change Golem's exported Go APIs and package boundaries. Use before adding public constructors, interfaces, options, result fields, model capabilities, tool contracts, or concurrency behavior.
---

# Golem API design

Design the smallest public contract that gives applications a useful, testable capability. Read `docs/foundation.md` and `references/api-decisions.md` before editing an exported API.

## Decision sequence

1. Write the caller's intended code in a test or package example.
2. List the required inputs, output, cancellation behavior, failure stages, and observable run evidence.
3. Decide whether a concrete type and option function work. Introduce an interface only if applications must substitute an implementation.
4. Place provider-specific types behind the `model` package boundary; do not leak SDK or transport types into core APIs.
5. Test the proposed contract against a fake implementation, including the primary failure path.
6. Update `README.md`; add an ADR before a difficult-to-reverse cross-package commitment.

## Non-negotiable checks

- Use `context.Context` first for every blocking operation.
- Preserve causal errors with wrapping; distinguish model, decode, tool, and policy stages.
- Use type parameters for caller-owned dependencies and outputs, not `map[string]any`.
- Keep result evidence normalized and ordered.
- Specify goroutine ownership and cancellation before adding concurrency.

Reject an API that is convenient only because it hides a provider, retry, validation, or lifecycle decision from the caller.
