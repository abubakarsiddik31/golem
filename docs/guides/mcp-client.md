# MCP client

## Purpose

Connect Golem agents to the Model Context Protocol ecosystem: the
`mcp` package speaks MCP to a server over stdio or streamable HTTP,
discovers its tools and returns them as ordinary `tool.Tool` values any
agent can register, offers its resources and prompts as typed client
operations the application drives, and answers the elicitation requests
a modern server sends mid-call.

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
  newlines; non-text content renders as placeholder lines. A tool that
  declares an output schema may answer with `structuredContent` and no
  content at all; the JSON is then the tool result text, verbatim.
- A result flagged `isError` rejects as a correctable
  `*model.ModelRetry`: the model sees the server's explanation and may
  retry with fixed arguments under the agent's tool retry budget.
- Protocol failures surface as the typed `*mcp.ProtocolError`;
  transport and cancellation errors preserve their cause — both fail
  the run at the tool stage, never as correctable rejections.

A tool's declaration carries more than its schema. `ToolInfo` exposes
the server's `Title`, its `OutputSchema` when one is declared, and its
`Annotations` — the protocol's behavioral hints. Each hint is a `*bool`
because "not declared" differs from an explicit false: where the
specification gives a default (destructive and open-world default to
true, read-only and idempotent to false), `nil` means the default
applies. Golem never enforces a hint; an approval flow may read
`DestructiveHint` to decide whether to ask before a call. `CallResult`
exposes `StructuredContent` alongside `Content`, so an application
that needs the JSON payload reads it without re-parsing the text.

The client is synchronous and serialized — one in-flight request at a
time, no goroutines — and honors the caller's context on every
operation. In a legacy session, while a call waits, a server-initiated
`ping` is answered inline, any other server-initiated request gets a
method-not-found reply, and notifications other than progress are
dropped. A modern session has no server-initiated requests: client
features arrive as input requests inside a result instead, and a server
that sends a request anyway fails the call.

`OnElicitation(handler)` answers the elicitation requests a modern
server sends inside an `input_required` result. With a handler
installed, every modern request declares the `elicitation` capability in
its metadata, so a compliant server may ask. The client calls the
handler for each request, retries the original call with
`inputResponses` keyed to the server's identifiers, and echoes
`requestState` verbatim; each retry is a new JSON-RPC request with a new
id. `Elicitation` carries the server's `Message`, its `Mode` (`form` or
`url`), the form's `RequestedSchema`, and the url mode's `URL`.
`ElicitationResult` carries the `Action` (`accept`, `decline`, or
`cancel`) and, for an accepted form, the `Content`. A handler error
fails the call; a decline or cancel is sent to the server, which decides
what happens next. The handler runs inline on the calling goroutine
under the client lock, and Golem never validates `Content` against
`RequestedSchema`.

Sampling and roots are deprecated by the `2026-07-28` revision and are
not implemented, so an `input_required` result that asks for either
fails with `*mcp.InputRequiredError` — as does an elicitation when no
handler is installed, or a result that asks for nothing at all. A server
that keeps asking is cut off after eight rounds.

`OnProgress(handler)` installs a callback for the progress a server
reports while a request is in flight. With a handler installed, every
request carries a fresh `progressToken` in its metadata — in both eras,
because `_meta` is the same mechanism in each — and each
`notifications/progress` whose token matches the request in flight is
delivered as `Progress{Completed, Total, Message}`. A notification
carrying any other token is dropped, so a late update from an earlier
call is never reported as the current one. The handler runs inline on
the goroutine that made the call, while the client holds its lock:
keep it quick, and do not call back into the client. Passing nil
removes it and stops asking for progress.

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
for the streamable-HTTP transport against a local endpoint. Run
`examples/mcp-elicitation` for the modern multi-round-trip flow: the
server's tool asks for confirmation, the client answers through
`OnElicitation`, and the call is retried with the answer and the echoed
state.

```go
transport, _ := mcp.NewStdio(mcp.StdioConfig{Command: "npx", Args: []string{"-y", "some-mcp-server"}})
// or remotely:
// transport, _ := mcp.NewHTTP(mcp.HTTPConfig{URL: "https://example.com/mcp"})
client := mcp.NewClient(transport)
defer client.Close()

client.OnElicitation(func(ctx context.Context, request mcp.Elicitation) (mcp.ElicitationResult, error) {
    // A real application prompts the user and honors a decline.
    // Never fetch or open a url mode URL on the user's behalf.
    return mcp.ElicitationResult{
        Action:  mcp.ElicitationAccept,
        Content: json.RawMessage(`{"name":"Ada"}`),
    }, nil
})

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
- `(*Client).OnProgress(handler func(mcp.Progress))` — `Progress{Completed, Total, Message}`; nil stops requesting progress
- `(*Client).OnElicitation(handler func(context.Context, mcp.Elicitation) (mcp.ElicitationResult, error))` — `Elicitation{Message, Mode, RequestedSchema, URL}`, `ElicitationResult{Action, Content}`; `ElicitationModeForm`, `ElicitationModeURL`, `ElicitationAccept`, `ElicitationDecline`, `ElicitationCancel`; nil stops declaring the capability
- `(*Client).ListTools(ctx) ([]ToolInfo, error)` — `ToolInfo{Name, Title, Description, InputSchema, OutputSchema, Annotations}`, `ToolAnnotations{Title, ReadOnlyHint, DestructiveHint, IdempotentHint, OpenWorldHint}`
- `(*Client).CallTool(ctx, name, arguments) (CallResult, error)` — `CallResult{Content, StructuredContent, IsError}`, `CallResult.Text()`
- `mcp.AsTools[Deps](ctx, *Client) ([]tool.Tool[Deps], error)`
- `(*Client).ListResources(ctx) ([]Resource, error)` — `Resource{URI, Name, Title, Description, MIMEType, Size}`
- `(*Client).ListResourceTemplates(ctx) ([]ResourceTemplate, error)` — `ResourceTemplate{URITemplate, Name, Title, Description, MIMEType}`
- `(*Client).ReadResource(ctx, uri) ([]ResourceContent, error)` — `ResourceContent{URI, MIMEType, Text, Blob}`
- `(*Client).ListPrompts(ctx) ([]Prompt, error)` — `Prompt{Name, Title, Description, Arguments}`, `PromptArgument{Name, Title, Description, Required}`
- `(*Client).GetPrompt(ctx, name, arguments) (PromptResult, error)` — `PromptResult{Description, Messages}`, `PromptMessage{Role, Content}`, `PromptContent{Type, Text, MIMEType, Data, Resource}`, `PromptMessage.Text()`
- `mcp.ProtocolError{Code, Message, Data}`, `mcp.HTTPStatusError{StatusCode, URL}`
- `mcp.UnsupportedProtocolVersionError{Requested, Supported}`, `mcp.InputRequiredError{Result, Method}`

## Gotchas

- The server subprocess inherits the process environment; `Env`
  entries are appended after it. Passing secrets to a server is the
  application's decision.
- Concurrent calls serialize at the client — parallel tool groups, and
  any resource or prompt read, run one server call at a time.
- A progress handler runs inline while the client holds its lock. A slow
  handler stalls the call, and calling back into the same client from
  it deadlocks; hand the update to your own queue or channel instead.
  Progress is per client, not per call, because calls are serialized.
- An elicitation handler runs inline under the same lock, so it may
  block on the context it is given but must not call back into the
  client. It is called once per request, in the server's key order.
  Elicitation is modern-era only: a legacy server's server-initiated
  `elicitation/create` still gets a method-not-found reply, because the
  legacy handshake does not declare the capability.
- For url mode, show the full URL and obtain explicit consent before
  returning accept — Golem never fetches or opens it. Golem also does
  not validate form `Content` against `RequestedSchema`; validate it in
  the handler if you rely on its shape, and never ask for passwords or
  API keys in form mode.
- `AsTools`, `ListTools`, `ListResources`, and `ListPrompts` are
  point-in-time snapshots; a server that changes them dynamically is
  re-read by calling again, not by push notification.
- Tool results carry only text; images and resources render as
  placeholders. A structured-only result becomes its JSON text, but the
  JSON is not validated against the declared output schema — validate
  it yourself if you rely on its shape. Annotations are server claims:
  a server that under-declares `destructiveHint` is still destructive.
  Prompt messages carry typed blocks — text, image, audio, embedded
  resource — but only text and text resources render through
  `PromptMessage.Text()`; inspect `PromptContent` for the rest.
  Sampling and roots are deprecated by the `2026-07-28` revision and are
  not implemented, so an `input_required` result that asks for either —
  or an elicitation with no handler — fails with
  `*mcp.InputRequiredError`.
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
- Decisions live in `docs/adr/0016-mcp-client-package.md`,
  `docs/adr/0032-mcp-resources-and-prompts.md`,
  `docs/adr/0034-mcp-tool-metadata.md`,
  `docs/adr/0035-mcp-progress-notifications.md`, and
  `docs/adr/0036-mcp-elicitation.md`.
