# Agent skills

## Purpose

Let a model load reusable, on-demand instructions from standard skill
directories: the `skills` package builds a tool that discovers
[Agent Skills](https://agentskills.io) (`<name>/SKILL.md` folders) and
returns a chosen skill's full instructions to the model when the task
matches the skill's description.

## When to use

Agents that accumulate procedural knowledge — release workflows, review
checklists, house style — as portable, version-controlled skill folders
shared across agents and products. Not for static background context:
when the instructions always apply, put them in the agent's
instructions instead. Not for knowledge retrieval at scale; skills are
a hand-curated catalog, not a search index.

## How it works

`skills.New[Deps](skills.Config{Dirs, MaxBodyBytes})` scans each
configured directory for `<name>/SKILL.md`, validates each file against
the Agent Skills format, and returns an ordinary `tool.Tool[Deps]`
named `skill`. The application resolves the paths — a server's config
directory today, an OS app directory or bundled path on desktop later;
the package never reads the working directory, home directory, or
environment.

Progressive disclosure happens in two places:

1. **Discovery (at construction).** Every `<name>/SKILL.md` is parsed
   and validated: `name` is required, ≤64 characters, lowercase
   alphanumeric with single hyphens, and must match its directory;
   `description` is required, ≤1024 characters; `compatibility`, when
   present, is ≤500 characters. Only each skill's name and description
   enter the model's context, as an `<available_skills>` catalog
   appended to the tool description. When two directories hold a skill
   of the same name, the first configured directory wins. Discovery is
   strict: one invalid file fails `New` with an
   `*skills.InvalidSkillError` naming the path, and a catalog with no
   valid skills fails too.
2. **Activation (per call).** The model calls `skill` with a `name`.
   An unknown or malformed name rejects with `*model.ModelRetry` (the
   error lists available names, so the agent's tool retry budget
   governs correction). A known name returns the skill's Markdown body
   wrapped in `<skill_content>` plus its base directory — relative
   references like `scripts/` and `references/` resolve against it —
   and a `<skill_files>` list of supporting files. Bodies are capped at
   `MaxBodyBytes` (default 1 MiB); a longer body is truncated, not
   failed, with a `[skills: body truncated at N bytes]` marker.

Bundled scripts and references are not executed by this tool. Point a
`fileread` tool (or `shell`) at the skill directories when the model
should read or run them; the returned base directory and file list tell
it where to look.

## Example

Run `examples/skills` — offline: a temp directory laid out in the
standard `.agents/skills` shape stands in for a skill pack and a
scripted fake model loads one skill.

```go
loading := skills.MustNew[struct{}](skills.Config{
    Dirs: []string{filepath.Join(workDir, ".agents", "skills")},
})

agent, err := golem.New[struct{}, string](client, decoder,
    golem.WithTools[struct{}, string](loading))
```

## API surface

- `skills.New[Deps](skills.Config) (tool.Tool[Deps], error)` / `skills.MustNew[Deps](skills.Config) tool.Tool[Deps]`
- `skills.Config{Dirs []string, MaxBodyBytes int64}`
- `skills.Discover(dirs []string) ([]Skill, error)` — the catalog without the tool
- `skills.Skill{Name, Description, Compatibility, Body, Dir}`
- `skills.ToolName`, `skills.ToolDescription` — the tool's stable identity and description preamble
- `skills.InvalidSkillError{Path string, Err error}`
- `skills.DefaultMaxBodyBytes`, `skills.MaxNameLength`, `skills.MaxDescriptionLength`, `skills.MaxCompatibilityLength`

## Gotchas

- The tool's name, argument schema, and description shape (including
  the catalog) are public contract; models and prompts depend on them
  staying stable.
- The frontmatter parser covers the YAML the format uses in practice —
  plain, quoted, and block scalars — not all of YAML. Anchors, flow
  style, and tags become literal text. `license` and `metadata` fields
  are ignored.
- Discovery is one level deep: `<dir>/<name>/SKILL.md` only. A bare
  `SKILL.md` directly inside a configured directory is not discovered,
  and the skill name must match its directory.
- Discovery is strict by design: a broken skill fails `New` instead of
  silently serving a stale catalog. Validate skills in CI with
  `skills.Discover` if authors edit them independently of the app.
- The catalog is fixed for the life of the tool value; skills added to
  disk after construction are invisible until a new tool is built.
- Where common tools live and their dependency rules were decided in
  `docs/adr/0015-common-tools-package.md`; the skills-specific
  decisions are in `docs/adr/0019-agent-skills-package.md`.
