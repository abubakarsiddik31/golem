# ADR 0037: MCP authorization — token source and challenge reporting

## Status

Accepted. Closes the MCP client breadth series; OAuth itself is
deliberately out of scope.

## Context

The fourth breadth item was OAuth for the streamable-HTTP transport.
The `2026-07-28` authorization specification is a substantial protocol:
protected-resource metadata discovery (RFC 9728), authorization-server
metadata (RFC 8414 or OIDC), client registration — Client ID Metadata
Documents preferred, Dynamic Client Registration deprecated — PKCE,
resource indicators (RFC 8707), issuer validation (RFC 9207), refresh
tokens, and a step-up flow that unions the granted scopes with each
`insufficient_scope` challenge and retries a bounded number of times.

Today the transport offers one thing: `HTTPConfig.Header`, copied at
construction. That is enough for a static token and nothing else. A
long-lived client cannot refresh a credential without rebuilding the
transport, and a server that refuses a request yields
`*HTTPStatusError` with no `WWW-Authenticate` detail, so an application
cannot tell an expired token from a missing scope or learn where the
authorization server lives.

The package's constraints are unchanged: the standard library only, no
provider dependency, no reflection or global state, no goroutine without
a documented lifecycle and cancellation, a small exported surface, and
additive API changes.

A conforming OAuth client would break several of them at once. The
authorization-code flow needs a redirect listener — a goroutine with a
lifecycle — or a browser launch, and it needs persistent token storage.
Client registration, PKCE, issuer validation, and resource binding are
security-critical: a subtle bug is a vulnerability, not a defect. All of
that would land in the same package that speaks JSON-RPC, for every
caller, including the ones that authenticate with a static header.

## Decision

Ship the boundary, not the protocol.

`HTTPConfig.TokenSource func(context.Context) (string, error)` is called
before every POST with that request's context. A non-empty token is sent
as `Authorization: Bearer <token>` and replaces any `Authorization` from
`Header`; an empty token sends the request with no `Authorization` at
all; an error fails the request before it is sent. The token source is
authoritative, so one field controls credentials instead of two. A token
that is not visible ASCII, or that has surrounding whitespace, is
rejected rather than sent, because a token carrying a newline could
inject a header.

A 401 or 403 is reported as `*mcp.AuthError`, carrying the underlying
`*HTTPStatusError`, the raw `WWW-Authenticate` value, and the parsed
`Challenge`. `Challenge` exposes the scheme, realm, RFC 6750 error code,
description, scope, and `resource_metadata` URL. `AuthError` unwraps to
the status error, so existing code that only inspects a status code keeps
working. Every other non-2xx status is still a plain `*HTTPStatusError`.

The header is scanned rather than split on commas, because one header may
carry several challenges and each challenge's parameters are themselves
comma-separated; quoted values may contain commas and escaped quotes.
Only the Bearer challenge is extracted. Token68 credentials, which no MCP
server uses, are not interpreted.

`Initialize` no longer falls back to the legacy handshake when the
discovery probe is refused. A 401 means the credentials were rejected,
which the legacy handshake would be too; falling back made a second
doomed request and buried the challenge the application needs.

The transport still never retries and never obtains a token. Discovery,
registration, PKCE, refresh, scope union, and the retry limit are the
application's, wired through any OAuth library it already trusts.

## Alternatives considered

**A full built-in OAuth 2.1 client.** The honest scope is a multi-commit
series, not one chunk: discovery, metadata, registration, PKCE, issuer
validation, resource binding, refresh, storage, and step-up, plus the
redirect listener and its lifecycle. It would be the largest
security-sensitive surface in the module, and every caller would carry it
whether or not it authenticates. It also conflicts with the rule against
adding a dependency for convenience, since a correct implementation
effectively wants a vetted OAuth library.

**Depend on `golang.org/x/oauth2`.** It is the right tool, and an
application should use it. Making the core module depend on it to offer
the same thing would add a non-stdlib dependency to every Golem user for
a feature most of them reach through a different mechanism.

**Keep `Header` only.** Static credentials work until they expire. The
gap is not "no OAuth" but "no way to change a credential between
requests", and one function field closes it.

**Report only the status code.** Without the challenge, an application
must parse `WWW-Authenticate` from a response it never sees, because the
transport consumes it. Surfacing the parsed challenge is the whole point
of the boundary.

**Return the raw header only.** Correct but useless: every application
would write the same parser, and most would write it wrong — splitting on
commas breaks on `error_description="expired, please renew"`.

**Retry automatically inside the transport.** A retry needs a token the
transport cannot mint. Retrying after a failed refresh would also need a
bound and a record of attempts, which is application policy.

**Send `Authorization` when the token source returns empty.** Then an
empty token would silently fall back to a stale static header, which is
the opposite of what the caller asked for.

## Consequences

An application authenticates with one field: assign the current token, or
return one from a refresh, and the next request uses it. When a server
refuses a request, the application gets the error code, the scope it
needs, and the metadata URL, and decides whether to re-authorize, retry,
or stop.

Because the transport does not retry, a step-up flow is two calls in
application code rather than one. That is visible in the example, and it
is the price of keeping the security-critical part where the application
can see it.

The `Header` path still works and is still the way to use a non-Bearer
scheme. A caller that sets both `Header`'s `Authorization` and a
`TokenSource` gets the token source's value, which the field
documentation states.

MCP's authorization story is still moving: client registration is
mid-migration from DCR to CIMD, and the specification defers several
details to extensions. Binding Golem to one implementation now would date
the package. The boundary survives those changes; a bundled client would
not.

If a real need appears for a bundled flow, it can be added additively —
another `HTTPConfig` field or a separate package that produces a
`TokenSource` — without changing this contract. That is the point of
deciding the seam first.
