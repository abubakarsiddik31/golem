# ADR 0053: MCP server mode — expose one agent as a tool

Date: 2026-11-16
Status: Accepted

## Context

Golem has a first-class MCP client (ADR 0016 and its follow-ups), but
users increasingly want the reverse: declare a specialist agent in Go
and let MCP clients call it. Issue #60 asks for stdio and HTTP
transports, an agent-as-tool mapping, and a stdlib-only implementation.

The frozen v1 seams already support this without changing the core:
`Agent.Run` is a plain method, `RunContext` carries typed dependencies,
`model` types are provider-neutral, and the existing `mcp.Transport`
interface describes framed JSON-RPC messages in both directions.

## Decision

A new `mcpserver` package wraps one `*golem.Agent[Deps, Output]` and
answers JSON-RPC 2.0:

- `initialize`, `ping`, `tools/list`, `tools/call`, and empty
  `resources/list` / `prompts/list`.
- One tool, configured with `Name`, `Description`, and `InputSchema`;
  the default schema takes a required `prompt` string.
- `Prompt` maps arguments to the agent prompt, `Format` renders the
  output, and `Deps` resolves typed dependencies per call.
- Agent failures return a tool result with `isError: true`; invalid
  arguments and unknown tools return JSON-RPC `-32602`.
- Legacy (`2025-06-18`) and modern (`2026-07-28`) revisions are
  negotiated by echoing a supported request version.

Transport is the existing `mcp.Transport`. `NewStdio` ships the
newline-delimited stdio implementation, and `Serve` handles one message
at a time. `ServeHTTP` implements the stateless streamable-HTTP request
shape: one POST, one JSON response; notifications return 202. SSE
streams, GET sessions, and server-initiated requests are not
implemented.

The server owns no authentication or authorization. Applications wrap
the HTTP handler and build `Deps` from the authenticated caller.

## Alternatives considered

- **`mcp/server` subpackage**: the import path is natural but the parent
  package is already the client and protocol home. A sibling
  `mcpserver` package keeps the client and server surfaces separate and
  avoids a package named `server`.
- **Full streamable HTTP with SSE**: needs event-stream framing, GET
  sessions, and session IDs. The stateless POST shape covers
  request/response tools and is enough for v1; SSE can be added
  additively.
- **Multiple tools or resources/prompts from one agent**: a single tool
  is the agent mapping. Exposing multiple tools would need a grouping
  model (the deferred toolset item).
- **Reuse the client `Client` type for both directions**: its handshake
  and request IDs are client-side. The server needs its own small
  dispatcher.
- **Server-side conversation state**: each call is an independent run;
  history and sessions belong to the application, consistent with the
  foundation's no-persistence-layer rule.

## Consequences

- New package and exported surface, additive to v1. The `mcp` client and
  core agent are untouched.
- Authentication, authorization, quotas, and audit remain application
  concerns; the guide says so.
- `Serve` is sequential; HTTP concurrency is bounded by the wrapped
  agent and application middleware.
- Tests cover initialize, tool listing/calling, protocol errors, failed
  agent results, dependency resolution, output formatting, HTTP status
  behavior, and a full stdio exchange.
