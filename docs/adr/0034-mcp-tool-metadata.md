# ADR 0034: MCP tool metadata and structured results

## Status

Accepted. Part of the MCP client breadth work; progress notifications
and the protocol's client features are separate decisions.

## Context

The MCP client bridges a server's tools into `tool.Tool[Deps]` and
reports each call as `CallResult`. Both types were narrow on purpose:
`ToolInfo` carried `Name`, `Description`, and `InputSchema`, and
`CallResult` carried only `Content` and `IsError`. That was enough to
call a tool, and it silently dropped three things the protocol has
since standardized:

- `title` — the human-readable name, distinct from the programmatic
  `name`.
- `annotations` — behavioral hints: read-only, destructive,
  idempotent, open-world, plus a title.
- `outputSchema` and its matching `structuredContent` — a JSON result
  contract for tools that return data rather than prose.

The gap matters in two places. An approval flow cannot ask a good
question without knowing whether a call is destructive, and a tool that
answers with structured data loses its payload when bridged, because
the bridge returns `Content` text only.

The constraint is the package's existing contract: `AsTools` returns
the server's schema verbatim, the client stays synchronous and
goroutine-free, and the package depends only on the standard library,
`model`, and `tool`. Adding fields is additive; changing what
`AsTools` returns is not.

## Decision

Surface the protocol's declaration and result metadata, and never
enforce it.

`ToolInfo` gains `Title`, `OutputSchema`, and `Annotations`.
`ToolAnnotations` mirrors the specification's five hints, each a
`*bool` for the four booleans. Pointers are deliberate: the protocol
distinguishes "not declared" from an explicit `false`, and two hints
have non-false defaults — destructive and open-world default to true,
read-only and idempotent to false. A `nil` hint therefore means "the
specification's default applies", and a caller that needs a concrete
answer resolves it itself. Golem never reads the hints on a run: they
are data for the application, not policy for the runtime.

`CallResult` gains `StructuredContent`, the raw JSON payload.
`CallResult.Text()` falls back to it only when the result carries no
content at all, so a structured-only result still reaches the model as
tool text while a result with both keeps its prose. The bridge does
not validate the payload against `OutputSchema`; the schema is
advisory, the payload is server-produced and untrusted, and validation
belongs to the application that chose to rely on the shape.

`AsTools` keeps its existing signature and behavior. It gains no
option, no wrapper type, and no new failure mode: a server that
declares nothing behaves exactly as before.

## Alternatives considered

**Plain `bool` hints.** Simpler to read, but loses the distinction
between "the server said false" and "the server said nothing", and
makes `destructiveHint`'s true default unrepresentable. An approval
flow would have to guess.

**Enforcing hints in the runtime** — for example, routing any
destructive tool through the deferred-approval mechanism — would make
a server's self-description into a security control it can trivially
under-declare. Hints inform the application; they do not bind it.

**A `tool.Tool` field for annotations.** The bridge would have to
decide which hints map onto Golem's own vocabulary, and the mapping
would then be compatibility surface. Metadata stays on `ToolInfo`,
where the application reads it before registering tools.

**Pretty-printing structured content into the text result.** Rewriting
server JSON breaks byte-for-byte expectations and costs an encode on
every call. Verbatim is honest and cheap.

**A typed result per output schema.** Golem does not infer schemas by
reflection; generating a Go type from a server's JSON Schema is the
application's decision, not the client's.

## Consequences

Applications can read a tool's declaration before calling it and can
recover structured payloads without re-parsing text, while every
existing call site keeps working. The three fields become compatibility
surface: they are additive, and a future field follows the same rule.

The fallback changes `Text()` only for results that previously
rendered as an empty string — a structured-only result used to reach
the model as nothing at all. That is a behavior correction, and it is
documented in the guide.

Because the client does not validate `StructuredContent`, a server can
return JSON that does not match its declared `OutputSchema`; the guide
says so, and applications that depend on the shape validate it
themselves. Hints can be wrong in the same way, which is why nothing
in the runtime trusts them.
