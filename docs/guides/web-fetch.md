# Web fetch

## Purpose

Give a model live web content: the `webfetch` package builds a tool that
GETs an `http(s)` URL and returns the response as text the model can
read. It is Golem's first common tool.

## When to use

Looking up documentation, summarizing a page, grounding an answer in
current web content. Not for endpoints that need authentication or
cookies (the tool sends none), for binary formats (rejected up front),
or for data an application can fetch itself and hand the model as a
dependency — when the caller controls the fetch, an ordinary tool with
typed arguments beats handing the model a URL.

## How it works

`webfetch.New[Deps](webfetch.Config{...})` returns an ordinary
`tool.Tool[Deps]` named `web_fetch` whose single required argument is a
`url` string. The tool takes the caller's context, so agent tool
timeouts and run cancellation govern the fetch. One call:

1. Validates the arguments — a missing, non-string, empty, or non-http
   `url` rejects the call with `*model.ModelRetry`, so the agent's tool
   retry budget governs correction.
2. GETs the URL through the configured `*http.Client` with the
   configured `User-Agent`. Redirects follow the client's default
   policy.
3. Accepts only 2xx: any other status fails at the tool stage with
   `*webfetch.StatusError` carrying the status code and final URL.
4. Reads the body up to `MaxBytes` (default 1 MiB). A longer body is
   truncated, not failed; the result ends with a
   `[webfetch: response truncated at N bytes]` marker.
5. Reduces the body to text by media type: HTML and XHTML are stripped
   to their visible text — comments, script and style contents, and
   markup dropped, character entities unescaped, block-level tags
   separating lines (extraction is best-effort, not a rendering
   engine); `text/*`, JSON, XML, YAML, CSV, and Markdown pass through
   unchanged; anything else fails at the tool stage with
   `*webfetch.UnsupportedContentTypeError`, including responses with no
   content type at all.

Network-level failures — including context cancellation and the
`Timeout` deadline — fail at the tool stage with the source error
preserved; they are never correctable rejections.

## Example

Run `examples/web-fetch` — offline: a local test server stands in for
the web and a scripted fake model requests the fetch.

```go
fetch := webfetch.MustNew[struct{}](webfetch.Config{Timeout: 5 * time.Second})

agent, err := golem.New[struct{}, string](client, decoder,
    golem.WithTools[struct{}, string](fetch))
```

## API surface

- `webfetch.New[Deps](webfetch.Config) (tool.Tool[Deps], error)` / `webfetch.MustNew[Deps](webfetch.Config) tool.Tool[Deps]`
- `webfetch.Config{HTTPClient *http.Client, Timeout time.Duration, MaxBytes int64, UserAgent string}`
- `webfetch.ToolName`, `webfetch.ToolDescription` — the tool's stable identity
- `webfetch.StatusError{StatusCode int, URL string}`
- `webfetch.UnsupportedContentTypeError{ContentType string, URL string}`
- `webfetch.DefaultUserAgent`, `webfetch.DefaultMaxBytes`, `webfetch.DefaultTimeout`

## Gotchas

- The tool's name, description, and argument schema are public
  contract; models and prompts depend on them staying stable.
- Bodies are assumed UTF-8: charset parameters are not transcoded (the
  package is standard-library only).
- `Timeout`, when non-zero, bounds one fetch with a context deadline on
  top of any client timeout; zero leaves the deadline to the caller's
  context and the agent's tool timeout. The default client (used when
  `HTTPClient` is nil) carries a 30-second timeout.
- The package depends only on the standard library, `model`, and
  `tool` — never on the root package or a provider — so it can be
  adopted without pulling anything else in.
- Decisions live in `docs/adr/0015-common-tools-package.md`.
