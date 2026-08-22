# ADR 0015: Common tools package

## Status

Accepted.

## Context

Golem ships only the tool *contract* (`tool.Tool[Deps]`); every tool so
far is application code. Frameworks in this space ship a small set of
common tools — fetch a URL, read the clock, run a command — that most
agents want. The first one, an HTTP fetch tool with text extraction, is
next on the roadmap, and where common tools live is a package-boundary
commitment: moving or reshaping a public package after v1 breaks users.

## Decision

Common tools ship as separate top-level packages, one per capability,
starting with `webfetch`. Each package:

- depends only on the standard library, `model`, and `tool` — never on
  the root package or a provider;
- exposes a `Config` struct plus `New[Deps]`/`MustNew[Deps]` returning
  an ordinary `tool.Tool[Deps]`, so the tool composes with any agent
  dependency type and every existing option (deadlines, retry budgets,
  parallel policy);
- is testable offline — network I/O goes through an injectable
  `*http.Client` and is covered by `httptest` servers, never the real
  network;
- treats its tool name, description, and argument schema as public
  contract: models and prompts depend on them staying stable.

Core stays tool-free. A convenience that would pull a dependency or
global into the core module is out of scope by definition.

## Alternatives considered

Putting common tools in the root package couples them to the agent API
and grows the v1 freeze surface with every tool. A single `tools`
package next to the `tool` contract invites an unbounded grab-bag and a
name that reads as a typo apart. Interface-based tools (`func New()
Tool`) without the dependency parameter would not compose with typed
agents. Shipping tools in the repo but out of the module (a `tools/`
directory with its own go.mod) fragments versioning for no benefit at
the current size.

## Consequences

Each tool adds a small public package to document, index, and keep in
the v1 freeze set — the cost is accepted per capability, and any second
tool reuses this decision rather than reopening it. Tool packages can
be adopted selectively: an application that wants none of them sees no
dependency or import cost. Extraction quality (HTML to text, media
handling) is best-effort and documented as such in each guide; the
contract is the tool boundary, not pixel-accurate rendering.
