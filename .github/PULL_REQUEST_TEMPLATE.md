<!-- One Conventional Commit subject should summarize this pull request, for
     example: feat(core): add typed agent configuration. -->

## What changes?

What behavior changes and why. Link any related issue.

## Checklist

- [ ] `gofmt`, `go build ./...`, `go test ./...`, and `go vet ./...` all pass.
- [ ] Tests cover the new behavior and its primary failure path, with provider
      calls faked — no live provider or network access.
- [ ] User-facing changes update the owning guide in `docs/guides/`, the
      matching example in `examples/`, and the README index where needed.
- [ ] Public API changes stay small, documented, and free of provider-specific
      types; difficult-to-reverse decisions add or revise an ADR.
