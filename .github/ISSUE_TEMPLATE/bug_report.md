---
name: Bug report
about: Something in Golem behaves incorrectly or fails
labels: bug
---

**What happened?**

A clear description of the bug, including any panic or error output. If a
`RunError` came back, include its stage (`model`, `tool`, `decode`, `loop`,
`usage`) and the preserved cause.

**What did you expect?**

**Reproduction**

The smallest program that shows the bug. A scripted `testmodel` fake is
preferred for execution-loop bugs; provider-specific bugs can name the provider
and request shape instead of using live credentials.

```go
// ...
```

**Environment**

- Golem version: <!-- tagged release or commit -->
- Go version: <!-- `go version` -->
- Provider and model (if applicable):
