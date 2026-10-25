# ADR 0033: Tool search for large toolsets

## Status

Accepted.

## Context

Every registered tool is advertised on every request. That is fine at
five tools and painful at fifty: tool definitions sit at the front of
the prompt, so a large catalog costs input tokens on every turn and
degrades tool selection — models start picking the wrong tool once the
list passes roughly thirty to fifty entries. The last open pre-freeze
roadmap item is model-driven discovery over such catalogs.

The catalog is not the model's to invent. Every tool an agent can ever
reach must be declared by the application up front, with a name, a
description, and a schema; deferring only decides which declarations
enter the request. That distinction is the whole design: **deferral is
about context, not registration.**

Prior art agrees on the mechanism and splits on execution. Pydantic AI
prefers provider-native search (Anthropic BM25/regex, OpenAI Responses
`tool_search`) and falls back to a local `search_tools` function tool
elsewhere, tracking discovered tools in typed history parts. Provider-
native search is a separate Golem item — native provider-executed tools
— and would put provider capability types in the core. What remains is
the local fallback, which needs no provider cooperation and works on
every model Golem supports.

## Decision

Deferral is an agent option, discovery is a runner-owned function tool.

`golem.WithDeferredTools[D, O](tools ...tool.Tool[D])` registers tools
that are declared but not advertised. `golem.WithToolSearchLimit(n)`
caps matches per search (default 5). When at least one tool is deferred,
the runner synthesizes one tool, `search_tools`, and advertises it
alongside the eager tools.

The search tool takes `{"query": string}` and returns text: one line per
match, `name(arg1, arg2): description`, best first, and a line saying
the matches are now available. A search with no matches returns normally
— it is a successful call, spends no retry budget, and says so. The
eager tools and `search_tools` are always advertised; a matched tool is
appended to the advertised set from the next request onward, in
declaration order after the eager specs, so the ordering is stable
rather than discovery-ordered.

Matching is deterministic and stdlib-only: the query is split into
lowercase alphanumeric tokens, and each token scores against a tool's
name tokens (weight 3), schema property names (weight 2), and
description words (weight 1). A tool's score is the sum over distinct
query tokens; a query token that only prefixes a catalog token scores
one less than an exact hit, so `calc` still reaches `calculator`.
Matches are ranked by score, then by name. There is no embedding index,
no fuzzy distance, and no model call in the search path.

**Discovery state is derived from history, not stored.** Before each
turn the runner scans the messages for `search_tools` calls, re-runs the
matcher on each call's arguments, and unions the results. Reveals
therefore survive the agent's output-correction rounds and a resumed run
that carries the exchange in its history, with no hidden state, no
shared mutable field, and no per-run object to thread through the API.
The cost is one scan per turn over a small history.

A call to a deferred tool that has not been revealed fails the run at
the tool stage with an error naming the remedy — the tool is declared
but not advertised, so the model cannot have known its schema. Calls to
names the agent never declared keep their existing terminal error.
Deferred tools are still dispatched from the full declared set, so a
reveal never changes dispatch, only advertisement.

`WithToolChoice` and `WithDeferredTools` are mutually exclusive;
construction fails when both are set, because pinning one tool and
hiding others describe incompatible requests.

## Alternatives considered

**One bridged tool per deferred tool** would keep the model's catalog
tiny but moves the choice of schema into the model's hands and cannot
express "search this catalog".

**An embedding index over tool descriptions** needs a dependency, a
vector store, and a model call, and makes discovery non-deterministic —
against the core's rules and impossible to test offline.

**Provider-native search** is the better wire on Anthropic and OpenAI
Responses, but it requires provider capability modeling in the core and
a per-provider fallback matrix. It is deliberately left to the
native-tools item; the local path is the one every model shares.

**Application-side pre-selection** (choose the tool subset per request
from application state) is a different feature — per-step tool
preparation — and does not give the model a way to recover when the
application guessed wrong.

**A shared state object on the Agent** (or in `ToolConfig`) would carry
reveals across runs without replay, but makes concurrent runs of one
agent interfere and adds lifecycle the design does not need.

## Consequences

A deferred tool costs one line of prompt text instead of a full schema
until it is matched, and the model pays one `search_tools` round trip to
unlock it. Adding a revealed tool changes the tool block, so providers
that cache the request prefix will miss the cache from the tool
definitions onward on the turn a reveal lands — a documented cost of
the local strategy, absent only under native search.

Search quality is keyword matching: a tool whose description does not
share a word with the query will not be found, and a search result is
the only path to a hidden tool. Applications therefore need honest
descriptions and names; the guide says so. The matcher, the default
limit, and the `search_tools` name become compatibility surface — a
future custom matcher must be additive, and the name is reserved for
agents that defer tools.

Discovery is per-run state reconstructed from history: a run that
starts from a sanitized or truncated history starts with only the eager
tools, and the model searches again. That is the documented behavior,
and it keeps the frozen surface free of a discovery-state type.
