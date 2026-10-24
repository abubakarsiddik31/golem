# ADR 0032: MCP resources and prompts as client operations

## Status

Accepted.

## Context

ADR 0016 shipped the MCP client tools-only and deferred the rest of the
protocol. Tools map cleanly onto `tool.Tool`, which is why the bridge
exists: the model decides when to call one. Resources and prompts do
not have that shape, and pretending otherwise would misfit them.

A resource is a file, record, or blob a server publishes at a URI.
MCP is explicit that resources are application-driven: the host decides
what to load, when, and how to put it in front of the model. Nothing in
the protocol says a model should discover and fetch them on its own, and
a `read_resource` tool would hand that decision to the model — turning
an application's curation choice into a tool call the model makes with
an invented URI. The same reasoning applies to prompt templates: a
prompt is a named, parameterized message list the *application* chooses
to render, typically as the opening turn of a conversation, not a
capability the model invokes.

Upstream reads the split the same way: Pydantic AI's MCP toolset exposes
`list_resources`, `list_resource_templates`, and `read_resource` as
toolset methods, with tools and prompts loaded separately.

The client is also the place where the era difference already lives.
Legacy and modern servers answer `resources/*` and `prompts/*` the same
way, and the modern transport's header mirroring already derives
`Mcp-Name` from `params.uri` for `resources/read` and from `params.name`
for `prompts/get` (ADR 0016's dual-era work). No new transport work is
needed — only typed request and result shapes.

## Decision

Resources and prompts become typed `*Client` operations, not bridged
tools.

`mcp/resources.go` adds `Resource`, `ResourceTemplate`, and
`ResourceContent` with `(*Client).ListResources`,
`(*Client).ListResourceTemplates`, and `(*Client).ReadResource(ctx,
uri)`. `mcp/prompts.go` adds `Prompt`, `PromptArgument`,
`PromptResult`, `PromptMessage`, and `PromptContent` with
`(*Client).ListPrompts` and `(*Client).GetPrompt(ctx, name,
arguments)`. Every list method follows pagination cursors to
exhaustion, the way `ListTools` already does, through one shared
`paginate` helper.

The wire shapes stay close to the specification so a server's fields
survive unchanged:

- `resources/list` and `prompts/list` return their items plus
  `nextCursor`; `resources/templates/list` returns `resourceTemplates`.
- `resources/read` takes `{uri}` and returns `{contents}`, a list
  because one URI may resolve to several pieces. `ResourceContent`
  carries `Text` or `Blob`: `encoding/json` decodes the protocol's
  Base64 `blob` string into bytes, so no manual decoding is needed and
  the two members are never both set.
- `prompts/get` takes `{name, arguments}` and returns `{description,
  messages}`. `PromptMessage.Content` is the protocol's content-block
  union reduced to the fields Golem can carry: text, MIME type, decoded
  data, and an embedded `*ResourceContent`. `PromptMessage.Text()`
  renders the text-bearing members and names anything else as a
  placeholder, matching `CallResult.Text()`.

Empty arguments are a caller mistake, not a model mistake: an empty
resource URI or prompt name fails locally with a plain error before a
request is sent, and no operation is retried or corrected. All methods
require `Initialize` and honor the caller's context.

Deliberately out of scope, unchanged from ADR 0016: sampling,
elicitation, roots, resource subscriptions, completion, and the
`x-mcp-header` tool-parameter extension.

## Alternatives considered

Bridging `resources/read` into a `tool.Tool` would make the model choose
URIs and load arbitrary server content into its own context. That is
the wrong default for a capability the protocol assigns to the host,
and it hides the application's curation decision inside a tool call.
Bridging prompts into tools has the same problem plus a worse one:
prompt templates are instructions, and a model-invoked prompt is an
authority the model should not grant itself.

Returning a single `ResourceContent` from `ReadResource` would discard
the protocol's multi-item result. Folding prompts into `model.Message`
would couple `mcp` to the root package's conversation types, which
ADR 0016 forbids; `PromptResult` is a protocol value the application
converts.

Auto-decoding `blob` by hand, or exposing it as a Base64 string, would
either duplicate `encoding/json` or push decoding onto every caller.

## Consequences

Public surface grows additively: three resource types and three
methods, five prompt types and two methods, plus `PromptMessage.Text()`.
No options, no interfaces, no goroutines — the client stays synchronous
and serialized, so these operations queue behind in-flight tool calls
like every other request.

Applications now own the resource and prompt policy: fetch what the
conversation needs, cache it, and render prompt messages into their own
history. Golem does not auto-inject either, which keeps the frozen
surface small and the trust model explicit — server-published content
is still untrusted input and should meet `SanitizeHistory` when it
crosses a boundary.

Only text-bearing prompt content renders to text; images, audio, and
resource links surface as typed blocks the application can inspect but
`Text()` names as placeholders. Servers that change resources or
prompts dynamically still require a fresh list call, because no
subscription is opened.
