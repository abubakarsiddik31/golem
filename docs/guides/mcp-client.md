# MCP client

## Purpose

Connect Golem agents to the Model Context Protocol ecosystem: the
`mcp` package speaks MCP to a server over stdio, discovers its tools,
and returns them as ordinary `tool.Tool` values any agent can
register.

## When to use

Reusing tool servers the ecosystem already ships — package-runner
based tools, local daemons, anything speaking MCP over stdio — instead
of re-implementing their capabilities as application tools. Not for
capabilities that are one `tool.New` away: a typed Go tool with the
process's own packages is simpler than a subprocess speaking JSON-RPC.
Remote servers are not reachable yet: streamable HTTP is the next
transport behind the same interface.

## How it works

`mcp.NewStdio(mcp.StdioConfig{Command, Args, Dir, Env})` starts the
server as a subprocess and owns its lifecycle — `Close` terminates it.
`mcp.NewClient(transport)` wraps any Transport; `Initialize` performs
the MCP handshake (announcing this client, recording the server's
identity in `Server()`), and must run before anything else.

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
operation. While a call waits, a server-initiated `ping` is answered
inline, any other server-initiated request gets a method-not-found
reply, and notifications are dropped.

## Example

Run `examples/mcp-client` — offline: the example binary doubles as a
minimal MCP stdio server and spawns itself.

```go
transport, _ := mcp.NewStdio(mcp.StdioConfig{Command: "npx", Args: []string{"-y", "some-mcp-server"}})
client := mcp.NewClient(transport)
defer client.Close()

_ = client.Initialize(ctx)
tools, _ := mcp.AsTools[struct{}](ctx, client)

agent, _ := golem.New[struct{}, string](model, decoder,
    golem.WithTools[struct{}, string](tools...))
```

## API surface

- `mcp.NewStdio(mcp.StdioConfig) (*StdioTransport, error)` — `StdioConfig{Command, Args, Dir, Env}`
- `mcp.Transport` — the `Send`/`Read`/`Close` boundary other transports implement
- `mcp.NewClient(mcp.Transport) *Client`
- `(*Client).Initialize(ctx)` / `(*Client).Server()` / `(*Client).Close()`
- `(*Client).ListTools(ctx) ([]ToolInfo, error)` — `ToolInfo{Name, Description, InputSchema}`
- `(*Client).CallTool(ctx, name, arguments) (CallResult, error)` — `CallResult{Content, IsError}`, `CallResult.Text()`
- `mcp.AsTools[Deps](ctx, *Client) ([]tool.Tool[Deps], error)`
- `mcp.ProtocolError{Code, Message, Data}`

## Gotchas

- The server subprocess inherits the process environment; `Env`
  entries are appended after it. Passing secrets to a server is the
  application's decision.
- Concurrent tool calls serialize at the client — parallel tool
  groups run one server call at a time.
- `AsTools` is a point-in-time snapshot; a server that changes its
  tools dynamically is re-bridged by calling it again, not by push
  notification.
- Only text content is carried; images and resources render as
  placeholders.
- On platforms where pipe reads cannot take deadlines (Windows),
  cancelling an in-flight stdio call is honored when the transport
  closes, not immediately.
- The package depends only on the standard library, `model`, and
  `tool` — never on the root package or a provider.
- Decisions live in `docs/adr/0016-mcp-client-package.md`.
