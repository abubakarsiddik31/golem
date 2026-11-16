# MCP server

## Purpose

Expose one Golem agent as a Model Context Protocol tool, so other
agents and MCP clients — Claude Desktop, Cursor, another Golem agent —
can call it.

## When to use

A specialist agent should be reachable outside its process: a support
triage agent, a document summarizer, an internal research assistant.
For the reverse direction — calling external MCP servers from a Golem
agent — use the [MCP client](mcp-client.md).

## How it works

`mcpserver.New` wraps a `*golem.Agent[Deps, Output]` as a server with one
MCP tool. Clients see `tools/list`, `tools/call`, `initialize`, `ping`,
and empty `resources/list` and `prompts/list` responses. A
`tools/call` resolves dependencies, runs the agent with the call's
prompt, and returns the formatted output as text.

The zero schema accepts `{"prompt": "..."}`. Configure `InputSchema`
and `Prompt` for a typed tool surface, `Format` to render the output, and
`Deps` to resolve the run's dependencies from the request context. Agent
failures become tool results with `isError: true`, so the calling model
sees the failure instead of a protocol error; malformed arguments and
unknown tools are JSON-RPC `-32602` errors.

Two transports ship:

- `NewStdio(in, out)` and `Serve` implement the stdio transport with
  newline-delimited JSON-RPC. The server handles one message at a time.
- `ServeHTTP` implements the stateless streamable-HTTP request shape:
  one POST carries a JSON-RPC message and the JSON response carries the
  result; notifications return 202. Server-initiated SSE streams and
  GET sessions are not implemented.

The server does not authenticate or authorize callers. Put the HTTP
handler behind your own middleware, and build `Deps` from the
authenticated identity; a tool call is an agent run, not a trust
boundary.

## Example

`examples/mcp-server` runs offline: it wraps a scripted agent, starts
the HTTP handler on a local test server, and sends initialize,
tools/list, and tools/call:

```go
server, _ := mcpserver.New(agent, mcpserver.Config[struct{}, string]{
    Name:        "assistant",
    Description: "Ask the example assistant a question.",
})
http.Handle("/mcp", server.ServeHTTP)
```

For stdio:

```go
server, _ := mcpserver.New(agent, cfg)
err := server.Serve(ctx, mcpserver.NewStdio(os.Stdin, os.Stdout))
```

## API surface

- `mcpserver.New[Deps, Output](agent *golem.Agent[Deps, Output], cfg mcpserver.Config[Deps, Output]) (*mcpserver.Server[Deps, Output], error)`
- `mcpserver.Config[Deps, Output]{Name, Description, Version, InputSchema, Prompt, Format, Deps}`
- `(*Server).Serve(ctx, mcp.Transport) error`
- `(*Server).ServeHTTP(http.ResponseWriter, *http.Request)`
- `(*Server).Handle(ctx, []byte) ([]byte, bool)`
- `mcpserver.NewStdio(in io.Reader, out io.Writer) *mcpserver.StdioTransport`
- `mcpserver.LegacyProtocolVersion`, `mcpserver.ModernProtocolVersion`

## Gotchas

- `Serve` is sequential per transport. The HTTP handler is safe for
  concurrent requests because the wrapped agent is, but one agent may
  run many calls at once; bound concurrency in middleware if needed.
- No SSE, server-initiated requests, sessions, resources, or prompts
  content in v1. An MCP client that requires a streamed response or a
  session header should use a future transport.
- A call has no conversation history: each tool call starts a fresh run.
  Store conversation state in your application if the tool must be
  multi-turn.
- The tool name must be unique within the server; v1 exposes exactly one
  agent tool.
- Authentication, authorization, quotas, and audit logging are the
  application's. The handler carries no implicit trust.
- The deciding design record is `docs/adr/0053-mcp-server-mode.md`.
