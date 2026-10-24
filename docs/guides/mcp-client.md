# MCP client

## Purpose

Connect Golem agents to the Model Context Protocol ecosystem: the
`mcp` package speaks MCP to a server over stdio or streamable HTTP,
discovers its tools and returns them as ordinary `tool.Tool` values any
agent can register, and offers its resources and prompts as typed
client operations the application drives.

## When to use

Reusing tool servers the ecosystem already ships — package-runner
based tools and local daemons over stdio, hosted services over
streamable HTTP — instead of re-implementing their capabilities as
application tools. Not for capabilities that are one `tool.New` away:
a typed Go tool with the process's own packages is simpler than a
subprocess or remote service speaking JSON-RPC.

## How it works

`mcp.NewStdio(mcp.StdioConfig{Command, Args, Dir, Env})` starts the
server as a subprocess and owns its lifecycle — `Close` terminates it.
`mcp.NewHTTP(mcp.HTTPConfig{URL, HTTPClient, Header})` reaches a
remote endpoint: every message is one POST, answered as a JSON body or
a text/event-stream whose events arrive one by one; the session header
a server assigns at initialize is captured and sent automatically, and
extra headers (such as `Authorization`) ride along on every request.
The server-to-client GET stream is not opened — pushes are not needed
for tool use.

`mcp.NewClient(transport)` wraps any Transport; `Initialize` negotiates
the protocol era, announces this client, and records the server's
identity in `Server()`. It must run before anything else.

The client is dual-era. It probes `server/discover` for the stateless
revision `2026-07-28`; a server that answers is modern, and every later
request carries the required per-request `_meta` (`protocolVersion`,
`clientInfo`, `clientCapabilities`) instead of a session. A server that
answers method-not-found — or any error that is not the modern
unsupported-version error — is legacy: the client falls back to the
`initialize` handshake for `2025-06-18` and completes it with the
`initialized` notification. `ProtocolVersion()` reports what was
negotiated. A modern server that names revisions it supports but not
`2026-07-28` fails `Initialize` with
`*mcp.UnsupportedProtocolVersionError`; the client does not retry a
legacy handshake against it, because only a modern server produces that
error.

Over streamable HTTP the era also decides transport behavior. Modern
requests carry `MCP-Protocol-Version`, `Mcp-Method`, and — for
`tools/call`, `resources/read`, and `prompts/get` — `Mcp-Name` headers
mirrored from the body, with names outside the safe ASCII set Base64
encoded behind the `=?base64?…?=` sentinel. A modern request never
carries `Mcp-Session-Id`, and a session header the server assigns is
ignored, because the modern revision is stateless.

`mcp.AsTools[Deps](ctx, client)` snapshots the server's tool list and
returns each as a `tool.Tool[Deps]` carrying the server's name,
description, and input schema verbatim — the server's schema is the
model-facing contract. Executing a bridged tool sends the model's raw
arguments to `tools/call`:

- Text content comes back as the tool result, items joined by
  newlines; non-text content renders as placeholder lines.
- A result flagged `isError` rejects as a correctable
  `*model.ModelRetry`: the model sees the server's explanation and may
  retry with fixed arguments under the agent's tool retry budget.
- Protocol failures surface as the typed `*mcp.ProtocolError`;
  transport and cancellation errors preserve their cause — both fail
  the run at the tool stage, never as correctable rejections.

The client is synchronous and serialized — one in-flight request at a
time, no goroutines — and honors the caller's context on every
operation. In a legacy session, while a call waits, a server-initiated
`ping` is answered inline, any other server-initiated request gets a
method-not-found reply, and notifications are dropped. A modern session
has no server-initiated requests: client features arrive as input
requests inside a result, and this client implements none of them, so a
result of type `input_required` fails with
`*mcp.InputRequiredError` rather than being silently dropped. A server
that sends a request anyway fails the call.

Resources and prompts are **not** bridged into tools, because the
protocol makes them application-driven: the host decides what to load
and what to render, and a model-chosen URI or prompt template would be
authority the model should not have. The application calls them
directly:

- `ListResources` returns `Resource{URI, Name, Title, Description,
  MIMEType, Size}`, following pagination cursors. `ListResourceTemplates`
  returns `ResourceTemplate{URITemplate, Name, Title, Description,
  MIMEType}`; expanding an RFC 6570 template is the caller's job.
- `ReadResource(ctx, uri)` returns `[]ResourceContent`, one item per
  piece the URI resolves to. Each item carries either `Text` or `Blob`;
  the protocol's Base64 `blob` string is decoded into bytes for you.
- `ListPrompts` returns `Prompt{Name, Title, Description, Arguments}`
  with `PromptArgument{Name, Title, Description, Required}`.
- `GetPrompt(ctx, name, arguments)` renders a template into
  `PromptResult{Description, Messages}`, where each `PromptMessage` has
  a `Role` and a `PromptContent` block. `PromptMessage.Text()` returns a
  text block's text or an embedded text resource's payload, and names
  anything else as a placeholder.

An empty resource URI or prompt name fails locally with a plain error
before any request is sent. Converting prompt messages into a
conversation, and deciding which resources enter context, stays with the
application — Golem does not auto-inject either.

## Example

Run `examples/mcp-client` — offline: the example binary doubles as a
minimal MCP stdio server and spawns itself. Run `examples/mcp-http`
for the streamable-HTTP transport against a local endpoint.

```go
transport, _ := mcp.NewStdio(mcp.StdioConfig{Command: "npx", Args: []string{"-y", "some-mcp-server"}})
// or remotely:
// transport, _ := mcp.NewHTTP(mcp.HTTPConfig{URL: "https://example.com/mcp"})
client := mcp.NewClient(transport)
defer client.Close()

_ = client.Initialize(ctx)
tools, _ := mcp.AsTools[struct{}](ctx, client)

agent, _ := golem.New[struct{}, string](model, decoder,
    golem.WithTools[struct{}, string](tools...))

// Application-driven: the host picks the resource and the prompt.
resources, _ := client.ListResources(ctx)
contents, _ := client.ReadResource(ctx, resources[0].URI)
fmt.Println(contents[0].Text)

prompts, _ := client.ListPrompts(ctx)
rendered, _ := client.GetPrompt(ctx, prompts[0].Name, map[string]string{"name": "Ada"})
for _, message := range rendered.Messages {
    fmt.Println(message.Role, message.Text())
}
```

## API surface

- `mcp.NewStdio(mcp.StdioConfig) (*StdioTransport, error)` — `StdioConfig{Command, Args, Dir, Env}`
- `mcp.NewHTTP(mcp.HTTPConfig) (*HTTPTransport, error)` — `HTTPConfig{URL, HTTPClient, Header}`
- `mcp.Transport` — the `Send`/`Read`/`Close` boundary other transports implement
- `mcp.NewClient(mcp.Transport) *Client`
- `(*Client).Initialize(ctx)` / `(*Client).Server()` / `(*Client).ProtocolVersion()` / `(*Client).Close()`
- `(*Client).ListTools(ctx) ([]ToolInfo, error)` — `ToolInfo{Name, Description, InputSchema}`
- `(*Client).CallTool(ctx, name, arguments) (CallResult, error)` — `CallResult{Content, IsError}`, `CallResult.Text()`
- `mcp.AsTools[Deps](ctx, *Client) ([]tool.Tool[Deps], error)`
- `(*Client).ListResources(ctx) ([]Resource, error)` — `Resource{URI, Name, Title, Description, MIMEType, Size}`
- `(*Client).ListResourceTemplates(ctx) ([]ResourceTemplate, error)` — `ResourceTemplate{URITemplate, Name, Title, Description, MIMEType}`
- `(*Client).ReadResource(ctx, uri) ([]ResourceContent, error)` — `ResourceContent{URI, MIMEType, Text, Blob}`
- `(*Client).ListPrompts(ctx) ([]Prompt, error)` — `Prompt{Name, Title, Description, Arguments}`, `PromptArgument{Name, Title, Description, Required}`
- `(*Client).GetPrompt(ctx, name, arguments) (PromptResult, error)` — `PromptResult{Description, Messages}`, `PromptMessage{Role, Content}`, `PromptContent{Type, Text, MIMEType, Data, Resource}`, `PromptMessage.Text()`
- `mcp.ProtocolError{Code, Message, Data}`, `mcp.HTTPStatusError{StatusCode, URL}`
- `mcp.UnsupportedProtocolVersionError{Requested, Supported}`, `mcp.InputRequiredError{Result}`

## Gotchas

- The server subprocess inherits the process environment; `Env`
  entries are appended after it. Passing secrets to a server is the
  application's decision.
- Concurrent calls serialize at the client — parallel tool groups, and
  any resource or prompt read, run one server call at a time.
- `AsTools`, `ListTools`, `ListResources`, and `ListPrompts` are
  point-in-time snapshots; a server that changes them dynamically is
  re-read by calling again, not by push notification.
- Tool results carry only text; images and resources render as
  placeholders. Prompt messages carry typed blocks — text, image, audio,
  embedded resource — but only text and text resources render through
  `PromptMessage.Text()`; inspect `PromptContent` for the rest.
  Sampling, elicitation, and roots are not implemented, so a modern
  `input_required` result fails instead of prompting.
- Resource and prompt content is server-published and therefore
  untrusted: treat a fetched resource like any other external input, and
  run it through `golem.SanitizeHistory` if it crosses a trust boundary
  into a conversation.
- The modern HTTP transport mirrors the standard headers
  (`MCP-Protocol-Version`, `Mcp-Method`, `Mcp-Name`) but not the
  `x-mcp-header` tool-parameter extension; a server that requires a
  parameter in `Mcp-Param-*` cannot be called.
- Era detection probes once with a five-second bound. A server that is
  slow to answer `server/discover` is treated as legacy, and a modern
  server that does not support `2026-07-28` fails `Initialize` rather
  than being retried over the handshake.
- Over HTTP, the default client has no wall-clock timeout so event
  streams are bounded only by the caller's context; supply
  `HTTPClient` for a hard bound. A legacy server that drops the session
  answers 404, surfacing as `*mcp.HTTPStatusError` — re-initialize a
  fresh client to recover. The modern revision has no session to drop.
- On platforms where pipe reads cannot take deadlines (Windows),
  cancelling an in-flight stdio call is honored when the transport
  closes, not immediately.
- The package depends only on the standard library, `model`, and
  `tool` — never on the root package or a provider.
- Decisions live in `docs/adr/0016-mcp-client-package.md` and
  `docs/adr/0032-mcp-resources-and-prompts.md`.
