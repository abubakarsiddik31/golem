# Golem coding-agent guide

Read `docs/foundation.md` and `.agents/skills/golem-development/SKILL.md` before changing code. Also use the focused local skill that matches the change:

- `.agents/skills/golem-api-design/SKILL.md` for exported APIs and package boundaries.
- `.agents/skills/golem-contract-tests/SKILL.md` for behavior changes and tests.

## Upstream reference cache

The official Pydantic AI docs are available locally at `reference/pydantic-ai/docs/` when the cache has been fetched. It is intentionally Git-ignored; use it for scoped design research, not as a source to copy wholesale. See `docs/upstream-references.md` to refresh it and verify the upstream revision before relying on a behavior.

## Operating rules

- Preserve the package boundaries documented in `docs/foundation.md`.
- Add behavior through tests first or alongside the implementation; provider calls must be faked in unit tests.
- Keep exported APIs small, documented, and free of provider-specific types.
- Take `context.Context` as the first parameter for operations that can block.
- Return typed or wrapped errors; do not log-and-continue in the core execution path.
- Do not introduce reflection, global mutable state, or goroutines without a documented lifecycle and cancellation behavior.
- Do not add a provider dependency to the core module merely for a convenience feature.

## Examples and guides

User-facing behavior is documented in three layers, and a change is only
complete when all three agree:

- `docs/guides/<topic>.md` is the source of truth for each capability.
  Start new guides from `docs/guides/TEMPLATE.md` and keep every heading.
  Every user-facing behavior change updates its guide in the same commit.
- `examples/` holds one runnable program per capability. Add one when
  introducing a user-facing feature (or extend the matching example), and
  keep guide snippets in sync with it. Provider-backed examples read
  their API key from the environment, print instructions, and exit when
  it is unset; at least one example must always run offline.
- `README.md` only indexes guides and examples. Update the index when
  adding either; do not duplicate guide detail in the README.

## Required checks

Run these checks for every code change and report their result precisely:

```bash
gofmt -w <changed-go-files>
go build ./...
go test ./...
go vet ./...
```

`go build ./...` guarantees the example programs compile. If changing a
public API, update its guide, update the README index, and add or revise
a runnable example or contract test.

## Commit boundaries

Commit one coherent, verified change at a time. Use Conventional Commit-style subjects, for example `feat(core): add typed agent configuration`. Never combine formatting churn with an unrelated behavior change.
