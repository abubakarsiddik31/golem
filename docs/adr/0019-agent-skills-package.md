# ADR 0019: Agent skills package

## Status

Accepted.

## Context

Agent Skills — a `<name>/SKILL.md` folder with YAML frontmatter and
Markdown instructions, per the open standard at agentskills.io — have
become the common way to package procedural knowledge for agents.
Coding agents (Claude Code, OpenCode, Crush, Gemini CLI, Codex) and
frameworks in our reference space (Pydantic AI) ship skills support.
Golem applications need the same capability without reimplementing
discovery, validation, and progressive disclosure on top of raw
`tool.Tool` values. The convention this repository itself uses for its
development skills, `.agents/skills/<name>/SKILL.md`, is the standard
layout.

## Decision

Skills ship as a common-tool package, `skills`, under the ADR 0015
rules: standard library plus `model` and `tool` only, `Config` plus
`New[Deps]`/`MustNew[Deps]` returning an ordinary `tool.Tool[Deps]`,
offline-testable, with the tool name, argument schema, and description
shape as public contract.

Specific commitments:

- **Standard directories, resolved by the application.** The tool takes
  explicit `Dirs` and never consults the working directory, home
  directory, or environment. Applications embed the tool in a server
  (config-driven paths) today and can move it to a desktop bundle (OS
  app directories or embedded files) without an API change.
- **Progressive disclosure via the tool description.** The catalog —
  each skill's name and description as `<available_skills>` XML — is
  appended to the tool description at construction. This keeps the
  feature a single tool value that composes with existing options, with
  no core changes: instructions are the one thing that changes at
  activation, and the root package already treats tool results as
  message evidence.
- **Activation by name.** The tool's only argument is the skill name;
  unknown names reject as correctable `*model.ModelRetry`. The result
  wraps the SKILL.md body plus the skill's base directory and a
  supporting-file list; it never executes bundled scripts — file or
  command tools cover that.
- **Strict, immutable discovery.** `Discover` validates every found
  SKILL.md against the format limits and `New` fails on any invalid
  file or an empty catalog. The catalog is fixed for the tool value's
  lifetime; rebuilding the tool reloads it.
- **Frontmatter subset, standard library only.** The parser supports
  plain, quoted, and block scalars — the forms real skill files use —
  and ignores every other field. Adding a YAML dependency to the module
  for the remaining corner cases is declined under the ADR 0015
  dependency rule.

## Alternatives considered

A core `WithSkills` option would couple the agent loop to filesystem
discovery and grow the v1 freeze surface for an optional capability; a
separate package composes instead. Injection through
`WithInstructionsFunc` (catalog in the system prompt) keeps the tool
pure but splits one feature across two agent options and a helper, and
the catalog would sit in every run's instructions whether or not the
model may act on it. Reusing the file-read tool with skill-path
arguments (the Crush approach) avoids a new package but pushes
validation and catalog formatting onto every application, and cannot
enforce the name/schema contract. A YAML dependency would match the
spec's grammar completely but breaks the standard-library-only rule
that keeps common tools cheap to adopt.

## Consequences

Applications gain skills in one constructor call; the exported surface
grows by one package to document, index, and freeze. Skill authors get
spec-level validation errors at construction rather than silent
staleness. The catalog-per-construction model means long-lived server
processes pick up edited skills only by rebuilding the tool —
acceptable at v0; hot reload would need a documented lifecycle and is
out of scope. Unsupported YAML corner cases surface as validation
errors with the file path, which CI can catch with `skills.Discover`.
