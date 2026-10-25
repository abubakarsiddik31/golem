# ADR 0036: MCP elicitation via multi round-trip requests

## Status

Accepted. Part of the MCP client breadth work; OAuth for the HTTP
transport is a separate decision.

## Context

The third breadth item was scoped as "sampling, elicitation, and roots".
The `2026-07-28` revision changed the ground underneath it. SEP-2577
deprecates sampling and roots, and the multi round-trip request (MRTR)
pattern replaces server-initiated requests with a stateless exchange:
the server answers `tools/call`, `prompts/get`, or `resources/read` with
an `input_required` result carrying a map of requests and an opaque
`requestState`; the client answers them and retries the original request
with `inputResponses` and the echoed state. Only those three requests
may produce an `input_required` result.

Elicitation is the live client feature in that set: a server asks the
person behind the client for a choice, a confirmation, or a credential.
Sampling and roots are deprecated, so newly adopting them would build on
a surface the protocol is retiring. The client already failed every
`input_required` result with `*InputRequiredError`; it implemented none
of the pattern.

The package's constraints are unchanged: the client is synchronous with
one in-flight request behind a mutex, introduces no goroutines, stays
comparable so `apidiff` accepts it, depends only on the standard
library, `model`, and `tool`, and keeps its exported surface small.

## Decision

Implement elicitation only, through MRTR, in the modern era.

`(*Client).OnElicitation(func(context.Context, Elicitation)
(ElicitationResult, error))` installs the handler; nil removes it. With
a handler installed, every modern request declares the `elicitation`
capability in `_meta`, as `{"form":{},"url":{}}`; without one it
declares nothing, so a compliant server will not ask. `Elicitation`
carries the server's `Message`, its `Mode`, the form's
`RequestedSchema`, and the url mode's `URL`. `ElicitationResult` carries
the `Action` — `accept`, `decline`, or `cancel` — and, for an accepted
form, the `Content`.

`call` becomes a bounded retry loop. It sends the original parameters,
reads one response, and on an `input_required` envelope calls the
handler once per request in key order, builds `inputResponses` keyed to
the server's identifiers, echoes `requestState` byte-for-byte when it
was sent, and retries with a fresh JSON-RPC id. The original parameters
are preserved across rounds. A server that keeps asking is cut off after
eight rounds, because the loop is otherwise unbounded work driven by the
remote side.

A handler error fails the call. A decline or cancel is forwarded, and
the server decides what the call does next. An unknown action is a
handler bug and fails the call. A request kind this client does not
implement — sampling or roots — fails with `*InputRequiredError`, whose
additive `Method` field names it. So does an elicitation with no handler,
and a result that carries neither requests nor state.

The client does not validate form `Content` against `RequestedSchema`,
and it never fetches, opens, or inspects a url mode `URL`. Both are the
application's decisions, stated in the handler's contract.

Legacy server-initiated `elicitation/create` is not adopted: it has a
different wire shape, its capability belongs in the `initialize`
handshake, and the modern pattern is the one the protocol is moving to.
A legacy server that sends one still gets a method-not-found reply.

## Alternatives considered

**Adopt sampling and roots too.** Both are deprecated by the revision
this client prefers. Sampling would also pull a model call into the MCP
client, coupling it to a provider or the root package; roots would hand
a server filesystem authority the application did not grant.

**Adopt legacy server-initiated elicitation.** It would double the wire
surface for an era the protocol is retiring, and the capability would
have to enter the initialize handshake. The modern path is the
forward-looking one; legacy support can be added additively if a real
server needs it.

**A per-call handler** — `CallToolWithElicitation(ctx, name, args, fn)`
— duplicates every operation that can return `input_required`, and
because calls serialize a client-level handler has the same effective
scope with one method.

**Unbounded retries.** The spec permits a server to ask repeatedly until
it has what it needs, but a buggy or malicious server could spin the
call forever. Eight rounds is generous for a real flow and finite.

**Validate the form against `RequestedSchema`.** The client would need a
JSON Schema implementation, and the schema is a restricted subset whose
validation the server performs anyway. Golem passes schemas through
everywhere else; the handler validates when it cares.

**Declare form mode only.** It is the conservative reading of the
capability, but it would leave url mode — the protocol's path for
credentials, payments, and OAuth — unreachable. The client itself never
acts on a URL: it hands the full URL to the handler and nothing else. The
handler's contract carries the consent and display obligations.

**Open the URL for the user.** The specification forbids pre-fetching or
opening a URL without explicit consent, and doing so would turn a server
claim into an action the user did not take.

## Consequences

An application can answer elicitation with one method and no concurrency
of its own, and a client without a handler behaves exactly as before:
it declares no capability and an `input_required` result still fails
with `*InputRequiredError`.

The handler sits on the critical path under the client lock, like the
progress handler: it may block on its context, a slow handler stalls the
call, and a re-entrant call deadlocks. The guide says so.

Url mode moves the consent obligation into the handler. The client
satisfies the part it can — never fetching or opening the URL — but it
cannot guarantee that an application shows the URL before accepting.
That is documented as the handler's responsibility, and it is why the
capability is declared only when a handler exists.

Because `Content` is not validated, a handler can submit a shape that
does not match the schema; the server rejects or re-asks. The
eight-round bound means a legitimate flow that needs more fails rather
than looping; it can be raised additively if a real server needs it.

`InputRequiredError` gains `Method`, which is additive, and its message
now names the unsupported request when one is known. Legacy elicitation
remains unimplemented and is called out in the guide.
