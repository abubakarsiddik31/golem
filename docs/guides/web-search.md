# Web search

## Purpose

Give the model a way to find pages on the open web, returning ranked
title, URL, and snippet results it can follow with
[Web fetch](web-fetch.md).

## When to use

A question whose answer is not in the prompt, the conversation, or a
local corpus — current events, documentation lookup, research before
fetching a page. When the URL is already known, `webfetch` is the
smaller tool.

## How it works

`websearch.New` returns the `web_search` tool: the model supplies a
query and an optional result limit, and the tool returns a numbered
list of `Result{Title, URL, Snippet}` values rendered as text. An empty
result set becomes `No results.`.

The zero `websearch.Config` uses DuckDuckGo's HTML endpoint, which needs
no API key. `Config.Backend` replaces it with any implementation of the
one-method `websearch.Backend` port — Brave, Tavily, SearXNG, an
internal search service — and the tool never reads credentials from the
environment. The package ships no other backend, so provider keys and
their request shapes stay application-side.

`Config.MaxResults` (default 5, at most 20) caps both the backend and
the model's requested limit; a larger `limit` is clamped. Bad model
input — an empty query, a negative limit, malformed JSON — returns
`&tool.Failed` so the model can correct the call. A backend or network
failure is an infrastructure error and aborts the run at the tool stage.

The DuckDuckGo backend sends `Config.UserAgent`, honors `Timeout` and
the caller's context, unwraps its redirect links, skips sponsored
blocks, and bounds the response body. It parses the HTML endpoint's
markup; the endpoint is overridable for a proxy or an offline test
server.

## Example

[`examples/web-search`](../../examples/web-search/main.go) runs offline
against a fake backend and a scripted model:

```go
search := websearch.MustNew[struct{}](websearch.Config{Backend: myBackend})
agent, err := golem.New[struct{}, string](client, decoder,
	golem.WithTools[struct{}, string](search),
)
```

## API surface

- `websearch.New[Deps](websearch.Config) (tool.Tool[Deps], error)` and `websearch.MustNew[Deps]`
- `websearch.Config{Backend, MaxResults, HTTPClient, Timeout, UserAgent}`
- `websearch.Backend` — `Search(ctx, query, limit) ([]Result, error)`
- `websearch.Result{Title, URL, Snippet}`
- `websearch.DuckDuckGo{Client, Timeout, UserAgent, Endpoint}` — the keyless backend
- `websearch.StatusError` — a completed DuckDuckGo request outside 2xx
- Constants: `ToolName`, `ToolDescription`, `DefaultMaxResults`, `MaxResultsLimit`, `DefaultTimeout`, `DefaultUserAgent`

## Gotchas

- The DuckDuckGo backend parses HTML, not an API. Markup changes and
  rate limiting are outside Golem's control; production deployments that
  depend on search should implement `Backend` against a stable provider.
- Follow the provider's terms and set a descriptive `UserAgent`. The
  default names Golem; a custom one should identify your application.
- Search snippets are untrusted web content. Treat them as data, not
  instructions, and keep prompts that consume them framed as such.
- `web_search` returns URLs; it does not fetch pages. Pair it with
  `webfetch`, whose address guard refuses private and link-local
  targets.
- The deciding design record is [ADR 0046](../adr/0046-web-search-tool.md).
