# Contributing to Golem

Thanks for improving Golem. This guide covers the practical steps;
[AGENTS.md](AGENTS.md) holds the full coding rules and
[docs/foundation.md](docs/foundation.md) explains the architecture and package
boundaries your change must preserve.

## Getting set up

Golem targets Go 1.26.5 or newer and has no dependencies beyond the standard
library, so a normal checkout is all you need:

```bash
git clone https://github.com/abubakarsiddik31/golem.git
cd golem
go build ./...
go test ./...
```

The full suite is offline: provider calls are faked in unit tests, and no test
reads credentials or touches the network. Keep it that way — new tests must
not call a live provider either.

## Making a change

1. Run the required checks before every commit and fix anything they report:

   ```bash
   gofmt -w <changed-go-files>
   go build ./...
   go test ./...
   go vet ./...
   ```

2. Add or extend tests alongside the behavior change. Cover the primary
   failure path with `errors.Is` or `errors.As` so causal errors stay
   inspectable, and avoid assertions that depend on sleeps or scheduling luck.

3. Keep user-facing behavior documented in the same change: update the guide
   in [docs/guides/](docs/guides/) that owns the behavior, keep the matching
   runnable example in [examples/](examples/) in sync, and update the README
   index when adding either. A change is complete only when all three agree.

4. Write Conventional Commit subjects, for example
   `feat(core): add typed agent configuration`, and commit one coherent,
   verified change at a time.

## Design changes

New public API, package boundaries, and concurrency behavior follow the
conventions in
[.agents/skills/golem-api-design/SKILL.md](.agents/skills/golem-api-design/SKILL.md):
write the caller's intended code first, prefer concrete types and options over
interfaces, and keep provider-specific types behind the `model` package.
Decisions that shape public contracts are recorded as ADRs in
[docs/adr/](docs/adr/); expect a short ADR for anything difficult to reverse.

## Reporting issues

Open a GitHub issue with the Golem version, the provider and model if
applicable, and a minimal reproduction — a scripted `testmodel` fake is
preferred when the bug is in the execution loop. Security reports go through
[SECURITY.md](SECURITY.md), never a public issue.
