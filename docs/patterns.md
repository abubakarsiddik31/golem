# Golem design patterns

This document is the repository-level reference for recurring implementation choices. The shorter operating version lives in `.agents/skills/golem-development`.

## 1. Typed application values, validated model values

Use generics for dependencies and final output. Model output is untrusted input: convert it through an explicit decoder or validator before returning it to the application.

**Use:** `Agent[Deps, Output]`, `RunContext[Deps]`, `OutputDecoder[Output]`.

**Avoid:** `map[string]any` dependency bags, implicit JSON decoding, provider response types in public results.

## 2. Functional options with explicit required dependencies

Constructors receive the required model and output decoder. Optional behavior uses option functions. This makes invalid configurations impossible to overlook and preserves discoverability in Go documentation.

## 3. Ports and adapters

The core depends on provider-neutral interfaces it owns. Provider packages translate their SDK into the `model.Model` port. Keep authentication, endpoints, streaming transports, and SDK errors at that adapter boundary.

## 4. Evidence-carrying results

Return the output together with the normalized messages and usage needed to understand it. Later additions—tool calls, retries, and timing—should be appended as ordered run events rather than hidden behind log output.

## 5. Explicit orchestration policy

The runner owns the loop: call model, inspect response, invoke an allowed tool, append evidence, then continue or terminate. Retry limits, timeouts, and tool permissions belong to explicit policies. An adapter must not silently choose them.

## 6. Context-aware lifecycle

Use the caller's `context.Context` for models, tools, and streaming. If concurrency is added, document its owner, cancellation path, completion signal, and error delivery. Do not leave background goroutines in the core.

## 7. Contract-first tests

Use fakes to test core behavior without network access. Every new public contract should demonstrate the happy path and the most meaningful failure path. Provider tests belong in their adapters and must be opt-in.
