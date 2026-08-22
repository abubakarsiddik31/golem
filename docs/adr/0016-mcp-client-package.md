# ADR 0016: MCP client package

## Status

Accepted.

## Context

The roadmap's v0.5.0 theme is ecosystem interop, and its centerpiece
is a client for the Model Context Protocol: JSON-RPC 2.0 messages over
a transport, an `initialize` handshake, and tool discovery and calls.
MCP servers are how the wider ecosystem ships tools — package runners,
local daemons, hosted services — and bridging them into
`tool.Tool[Deps]` gives every Golem agent that ecosystem for free.
The design must not pull dependencies into the core, and once
applications register MCP tools the bridging semantics become a
compatibility contract.

## Decision

MCP support ships as a new top-level `mcp` package that, like the
common-tool packages, depends only on the standard library, `model`,
and `tool` — never on the root package or a provider.

The transport boundary is three methods: `Send(ctx, message)`,
`Read(ctx) message`, `Close()`, carrying one complete JSON-RPC message
each way. `StdioTransport` (a spawned server subprocess, newline-
delimited JSON on its stdin/stdout) comes first; streamable HTTP
follows behind the same interface. The transport owns its resource
lifecycle: `NewStdio` starts the process and `Close` terminates it.

The client is synchronous and serialized: one in-flight request at a
time behind a mutex, with no background goroutines. Each call sends
its request and reads until its response arrives; a server-initiated
`ping` is answered inline, any other server-initiated request gets a
JSON-RPC method-not-found reply, and notifications are dropped.
Cancellation flows from the caller's context through reads (poll
deadlines on stdio; `Close` tears the process down regardless).

Bridging is `AsTools[Deps](ctx, client)`: every server tool becomes an
ordinary `tool.Tool[Deps]` carrying the server's name, description,
and input schema verbatim — the server's schema is the model-facing
contract. Execution calls `tools/call` with the model's raw arguments;
the result's text content is the tool result, a tool result flagged
`isError` rejects as a correctable `*model.ModelRetry` so the model
can retry with fixed arguments, and protocol-level JSON-RPC errors
surface as the typed `*mcp.ProtocolError` at the tool stage — never
correctable.

## Alternatives considered

Putting MCP into the core couples the agent API to one ecosystem and
grows the v1 freeze surface. A goroutine-per-connection read loop with
a pending-request map supports concurrent calls but adds a lifecycle,
error routing, and shutdown story the current need does not require;
serialization is documented instead. Full protocol coverage —
resources, prompts, sampling, subscriptions, resumable streams — is
deferred: tools are the capability Golem agents need first, and the
narrow surface keeps the freeze set small. Silently returning tool
errors as results would hide the correction loop Golem already has.

## Consequences

Concurrent tool calls (parallel groups) serialize at the client:
correct, but one slow server call delays the others. Servers that
change their tool list dynamically get no push notification handling
— callers re-run `AsTools` for a fresh snapshot. Only text content is
carried; other content types render as placeholders. On platforms
where pipe reads cannot take deadlines (Windows), cancellation of an
in-flight stdio read is honored at `Close` time rather than
immediately. Each future transport implementation must preserve the
Send/Read/Close contract and the message-at-a-time framing.
