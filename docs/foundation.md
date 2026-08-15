# Golem foundation brief

## Goal

Give Go developers a small, dependable way to build AI agents whose important contracts are explicit and testable: dependency input, model interaction, tool calls, structured output, and run history.

The initial success criterion is simple: a developer can configure an agent with a model and typed dependencies, run it with a prompt, inspect the result, and test the behavior without a live provider.

## Product context

Pydantic AI is a useful reference point for developer experience: agents own instructions, tools receive run context, and an application receives a validated result. Golem should learn from those ideas, not mirror Python mechanisms.

| Python-oriented idea | Golem interpretation |
| --- | --- |
| Runtime data validation | Compile-time type parameters where possible; explicit validators at I/O boundaries |
| Decorator registration | Constructors and option functions |
| Dynamic dependency injection | Typed run context with an explicit dependency value |
| Exception-led control flow | Returned errors with enough structured context to classify and retry |
| Provider-specific integration | Small provider-neutral model interface and adapter packages |

## Non-goals for v0

- A provider SDK, web server, workflow engine, vector database, or persistence layer.
- Hidden background execution or implicit global configuration.
- Reflection-heavy tool discovery.
- A promise of exactly-once tool execution across process failures.

## Architecture rules

1. **Keep the public core small.** Add an interface only when applications genuinely need to swap an implementation.
2. **Make the boundary typed.** Use generics for agent dependencies and final output; validate untrusted model/provider data at the boundary.
3. **Accept context first.** Every operation that can wait, call a provider, or invoke a tool accepts `context.Context`.
4. **Preserve execution evidence.** A run result must retain messages, tool activity, model usage, and terminal cause without forcing a tracing backend.
5. **Separate description from execution.** Tool metadata must be inspectable without executing the tool.
6. **Make retries policy-driven.** The executor decides whether and how to retry; tools and models return classified errors.
7. **Build test doubles first.** The core must be usable with an in-memory model fake before a real provider adapter exists.

## Initial package boundaries

```text
golem
  Agent[Deps, Output]       public configuration and Run entry point
  RunContext[Deps]          dependency value and run metadata passed to tools
  Result[Output]            output, messages, usage, and terminal metadata
model
  Model                     provider-neutral Generate contract
  Request / Response         normalized conversational exchange
tool
  Tool[Deps]                name, description, schema, execution contract
internal/runner
  Loop                      model/tool orchestration; never imported by users
```

Start with a single-turn model execution path. Introduce tool-call looping only after the message and error contracts have real tests.

## Decision record practice

Write a short ADR in `docs/adr/` before making a cross-package commitment that would be painful to reverse: a new extension point, a serialized trace format, a concurrency model, or a provider capability model. Keep the ADR to the decision, alternatives, and consequences.
