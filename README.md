# Golem

Golem is a Go-first framework for building dependable AI agents: typed dependencies and outputs, explicit tools, composable models, and an observable execution loop.

It takes inspiration from the ergonomics of Python agent frameworks such as Pydantic AI, but follows Go's strengths instead: compile-time contracts, `context.Context`, explicit error handling, small interfaces, and standard-library-friendly integrations.

## Status

The project is at the foundation stage. The public API is intentionally small while the core execution contract is established.

## Direction

- Make a useful agent the shortest path: configure a model, declare tools, run with dependencies, get a typed result.
- Make important behavior explicit: model calls, tool execution, retries, usage, and validation are visible in the run result.
- Keep infrastructure replaceable: applications choose models, tracing, storage, and transport through narrow interfaces.
- Prefer Go-native composition over ports of Python metaprogramming.

Read [the foundation brief](docs/foundation.md) before proposing a new public abstraction. Contributor and coding-agent rules live in [AGENTS.md](AGENTS.md).

## Planned package shape

```text
golem/        Agent configuration and typed run API
model/        Provider-neutral model request/response contract
tool/         Tool declarations and execution contracts
internal/     Execution loop and non-public mechanics
```

## Development

```bash
go test ./...
go vet ./...
```

The module path is provisional until the private GitHub repository is created.
