# Tool search

## Purpose

Keep a large tool catalog out of every request. Tools are declared up
front but advertised only when the model searches for them, so a
fifty-tool agent pays for the handful it might use instead of every
schema on every turn.

## When to use

Use it when an agent declares enough tools that the catalog dominates
the prompt or degrades tool selection — roughly past a few dozen — and
most turns need only a few of them. Register a handful of tools with
`WithTools` instead: search adds a model round trip and a keyword
matcher between the model and a tool it could have called directly.

It is also not per-step tool pre-selection. The application cannot
choose the subset for each request; the model discovers it. And it does
not combine with `WithToolChoice`, which pins one tool.

## How it works

`WithDeferredTools[Deps, Output](tools ...tool.Tool[Deps])` declares
tools that are valid and dispatchable but withheld from the advertised
set. `WithTools` keeps registering eager tools. Every declared tool —
eager or deferred — is validated at construction: names must be unique
across both lists, and `search_tools` is reserved once anything is
deferred.

When at least one tool is deferred, the runner synthesizes one
`search_tools` tool and advertises it beside the eager tools. It takes
`{"query": string}` and returns text: one line per match,
`name(arg1, arg2): description`, best first, and a line saying the
matches are available from now on. A search with no matches is a
successful call — it spends no retry budget and says so. A malformed or
empty query is a correctable `*model.ModelRetry`, fed back under the
agent's tool retry budget (or ending the run at the tool stage when no
budget is configured).

Matching is deterministic and stdlib-only. The query splits into
lowercase alphanumeric tokens, breaking on separators and camel-case
boundaries. Each token scores against a tool's name tokens (weight 3),
its schema property names (weight 2), and its description words
(weight 1); a tool's score is the sum over distinct query tokens. A
token that only prefixes a catalog token scores one less than an exact
hit, so `calc` still reaches `calculator` without outranking a real
match. Matches rank by score, then by name, and stop at the limit —
five by default, set with `WithToolSearchLimit`.

Reveal is derived from history, not stored. Before every request the
runner scans the conversation for `search_tools` calls, re-runs the
matcher on each call's arguments, and unions the results. The
advertised set is the eager specs, then `search_tools`, then the
revealed specs in declaration order — stable rather than
discovery-ordered. Because the state is the history, a reveal survives
the agent's output-correction rounds and a resumed run that carries the
exchange; nothing is shared between concurrent runs.

A call to a deferred tool that has not been revealed fails the run at
the tool stage with an error naming `search_tools`: the tool is
declared, but the model could not have seen its schema. Once revealed,
the call dispatches from the full declared set like any other tool. The
search itself is an ordinary tool execution — it counts toward
`Result.ToolCalls` and usage limits, emits the usual tool events, and
honors the caller's context; there is no new run event.

## Example

`go run ./examples/tool-search` runs offline against `testmodel`. It
declares one eager tool and nine deferred ones, then scripts the model
to search for the invoice tool and call it:

```go
agent, err := golem.New[struct{}, string](client, decoder,
	golem.WithInstructions[struct{}, string]("Search for a tool before using one that is not listed."),
	golem.WithTools[struct{}, string](buildTool(eager)),
	golem.WithDeferredTools[struct{}, string](deferredTools...),
)
```

The first request advertises `get_current_time` and `search_tools`; after
the search, `get_invoice` joins the set. The example prints the
advertised count per request, the search result, and the answer.

## API surface

- `golem.WithDeferredTools[Deps, Output](tools ...tool.Tool[Deps])` — declare tools that stay unadvertised until searched.
- `golem.WithToolSearchLimit[Deps, Output](limit int)` — cap matches per search; zero uses the default of 5, negative is rejected at construction.
- `golem.WithTools[Deps, Output](tools ...tool.Tool[Deps])` — eager tools, advertised every request.

## Gotchas

Search quality is keyword matching. A tool whose name, arguments, and
description share no word with the query will not be found, and search
is the only path to a hidden tool. Name tools for the task and write
descriptions with the words a caller would use; a vague description
hides the tool as effectively as not declaring it.

`search_tools` is reserved for agents that defer tools; a declared tool
with that name fails construction. `WithDeferredTools` and
`WithToolChoice` are mutually exclusive, and `WithToolSearchLimit` has
no effect without deferred tools.

Revealing a tool changes the tool block, so providers that cache a
request prefix miss the cache from the tool definitions onward on the
turn a reveal lands. That is the documented cost of the local strategy;
provider-native search is a separate, post-v1 capability. Decisions
live in `docs/adr/0033-tool-search.md`.

Discovery lives in the history. A run that starts from a sanitized or
truncated conversation begins with only the eager tools, and the model
must search again. The limit caps one search, not the total number of
revealed tools: repeated searches can reveal the whole catalog.
