# ADR 0035: MCP progress notifications

## Status

Accepted. Part of the MCP client breadth work.

## Context

A tool call can take a while — a document conversion, a remote index
rebuild, a batch job — and MCP lets a server report how far along it is
through `notifications/progress`, correlated to the request by a
`progressToken` the client put in the request's `_meta`. The token is
the client's opt-in: a server that sees none has no obligation to
report anything.

The client dropped every notification. That was correct when nothing
consumed them, and wrong as soon as an application wants a progress
bar: the protocol's only interim signal was being thrown away while the
call blocked.

The constraints are the package's: the client is synchronous, holds one
in-flight request at a time behind a mutex, introduces no goroutines,
and must stay safe for concurrent use. Progress is inherently
asynchronous — it arrives between the request and its response — so the
design has to fit that into a synchronous read loop.

## Decision

Progress is a client-level callback, invoked inline in the read loop.

`(*Client).OnProgress(func(Progress))` installs a handler; nil removes
it. With a handler installed, every request carries a fresh integer
`progressToken` in its `_meta` — in the modern era alongside the
required revision and client identity, in the legacy era as the only
`_meta` entry, because `_meta` is the same extension mechanism in both.
Without a handler no token is sent, so a client that does not care
never asks a server to do the work.

The read loop already inspects every incoming message to find its
response. A `notifications/progress` message is decoded and delivered
as `Progress{Completed, Total, Message}` — the token is consumed, not
exposed, because the handler only ever runs during a call the
application itself started. A notification whose token does not match
the request in flight is dropped: a late update from an earlier call
must not be reported as the current one.

The handler runs on the goroutine that made the call, while the client
holds its lock. There is no queue, no buffering, and no goroutine. The
cost is that a slow handler stalls the call, and a handler that calls
back into the client deadlocks; both are documented in the guide.

## Alternatives considered

**A per-call handler** — `CallToolWithProgress(ctx, name, args, fn)` —
reads naturally at the call site but duplicates every operation that
can report progress (tools, resources, prompts, lists) and adds an
option to each. Because calls serialize, a client-level handler has the
same effective scope with one method.

**A background goroutine and a channel** would decouple the handler
from the lock, but it breaks the package's no-goroutine rule, needs a
lifecycle and a close path, and makes delivery order and cancellation
someone's problem. The inline callback keeps the whole feature inside
the existing loop.

**Collecting progress on `CallResult`** would give the caller a slice
of updates after the fact, which is exactly the wrong time — a progress
bar needs them during the call — and buffers without a bound.

**Delivering every notification regardless of token** is simpler but
misattributes a late update from a previous call, which is the one case
where a progress bar lies.

**Exposing the token to the handler** would let an application correlate
updates itself, but the client already guarantees the handler only sees
the request in flight. The token is an implementation detail of the
correlation.

## Consequences

An application can show progress for a long call with one method and no
concurrency of its own. A client that never installs a handler sends no
tokens and behaves exactly as before.

The handler is on the critical path: it runs under the client lock, so
its cost is added to every progress notification, and a re-entrant call
is a deadlock rather than an error. That is the price of keeping the
client goroutine-free, and the guide states it plainly.

Progress is advisory and may never arrive — a server is free to ignore
the token — so an application cannot use it as a liveness check.
Notifications that arrive while no call is in flight are read by the
next call and dropped by the token check; nothing buffers them.
