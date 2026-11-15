# ADR 0046: Web search tool — a backend port and a keyless default

Date: 2026-11-15
Status: Accepted

## Context

`webfetch` reads a URL the model already knows, but agents usually need
to discover URLs first. Issue #56 asks for a standard web-search common
tool: typed results, safe timeouts, no API-key requirement for the
default path, and no new module dependency.

Search providers differ in credentials, quotas, and response shapes.
Shipping a provider SDK or key handling in the core would violate the
zero-dependency and application-owns-credentials rules. Pydantic AI
solves the same problem with provider-native search where available and
a local DuckDuckGo fallback elsewhere.

## Decision

A new `websearch` package owns one ordinary tool, `web_search`, with a
one-method `Backend` port:

```go
Search(ctx context.Context, query string, limit int) ([]Result, error)
```

`Result` is `Title`, `URL`, `Snippet`. The tool validates model input,
clamps the limit to `Config.MaxResults`, truncates over-long backend
results, renders a numbered list, and turns bad input into
`&tool.Failed` while leaving backend failures as infrastructure errors.

The zero `Config` selects `DuckDuckGo`, a keyless backend over
DuckDuckGo's HTML endpoint. It honors context and an optional timeout,
sets a configurable User-Agent, unwraps redirect links, skips sponsored
blocks, and bounds the response body. `Config.Backend` replaces it with
any application implementation; no provider key or provider-specific
type enters the package.

The HTML parser is small and fixture-tested rather than a general HTML
engine: the module has no `golang.org/x/net/html` dependency, and the
tool only needs anchors, snippets, and class names.

## Alternatives considered

- **Require an API key** (Brave, Tavily): better data, but no offline
  default and credential plumbing in a common tool.
- **DuckDuckGo Instant Answer JSON API**: stable JSON, but it returns
  instant answers rather than ranked web results.
- **Provider-native search tools**: provider-specific request shapes
  and billing; deferred per the roadmap's native-tools item.
- **A full HTML parser dependency**: breaks the zero-dependency rule
  for one tool; the fixture parser covers the endpoint's markup.
- **Returning raw HTML to the model**: wastes context and invites
  prompt injection from markup; typed results are the point.

## Consequences

- New package and exported surface, additive to v1. The core module and
  existing tools are untouched.
- The DuckDuckGo backend can break when the endpoint's markup changes.
  The guide says so and directs production deployments to `Backend`.
- Search results are untrusted content. The guide frames them as data
  and points to `webfetch` for the address-guarded read step.
- Tests run offline: a fake backend for the tool contract, fixture HTML
  and `httptest` servers for the DuckDuckGo backend, plus status,
  cancellation, limit, and validation paths.
