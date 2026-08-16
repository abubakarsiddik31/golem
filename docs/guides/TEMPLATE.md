# Guide template

Copy this file when adding or revising a guide. Keep every heading; write
"None identified" rather than deleting a section that does not apply.
Guides are the source of truth for user-facing behavior — the README
links here and must not drift ahead of them.

```markdown
# <Feature title>

## Purpose

One or two sentences: what this capability is for.

## When to use

The situations that call for it, and — just as important — when a
simpler feature already covers the need.

## How it works

The behavior contract: what the run does, in order, and what is
observable afterwards. Mention error stages, budgets, and cancellation
where they apply.

## Example

A runnable path: the example command under `examples/` and/or a godoc
example, plus a minimal inline snippet. Inline snippets must stay in
sync with the example they summarize.

## API surface

The exported names a caller uses, one line each, in the shape callers
read them (e.g. `golem.WithTools[Deps, Output](tools ...tool.Tool[Deps])`).

## Gotchas

Footguns, defaults that surprise, and provider differences. Link the
deciding ADR when one exists.
```
