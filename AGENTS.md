# Golem coding-agent guide

Read `docs/foundation.md` and `.agents/skills/golem-development/SKILL.md` before changing code.

## Operating rules

- Preserve the package boundaries documented in `docs/foundation.md`.
- Add behavior through tests first or alongside the implementation; provider calls must be faked in unit tests.
- Keep exported APIs small, documented, and free of provider-specific types.
- Take `context.Context` as the first parameter for operations that can block.
- Return typed or wrapped errors; do not log-and-continue in the core execution path.
- Do not introduce reflection, global mutable state, or goroutines without a documented lifecycle and cancellation behavior.
- Do not add a provider dependency to the core module merely for a convenience feature.

## Required checks

Run these checks for every code change and report their result precisely:

```bash
gofmt -w <changed-go-files>
go test ./...
go vet ./...
```

If changing a public API, update `README.md` and add or revise an example or contract test.

## Commit boundaries

Commit one coherent, verified change at a time. Use Conventional Commit-style subjects, for example `feat(core): add typed agent configuration`. Never combine formatting churn with an unrelated behavior change.
