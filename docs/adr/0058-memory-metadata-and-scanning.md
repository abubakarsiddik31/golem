# ADR 0058: Memory write metadata and artifact scanning

Date: 2026-11-16
Status: Accepted

## Context

Memory persists model-written Markdown across runs. Two application needs
follow: provenance (who wrote the current version) and a cheap check for
content that should not be stored or reloaded, such as prompt-injection
phrasing or credential-exfiltration instructions. Pydantic AI Harness
ships both: `MetadataMemoryStore` for provenance and `ArtifactScanner`
for stored artifacts.

Golem's `Store` port and tools are small; both features can be optional
and additive without forcing every backend to implement them.

## Decision

**Metadata.** `memory.MetadataStore` gains `ReadMetadata`.
`MetadataStore` extends `VersionedStore` with
`WriteVersionedWithMetadata`, recording content and provenance in one
atomic step. `Memory` implements it; `FileStore` does not, because a
sidecar could not change atomically with the Markdown and the model's
file tools could edit it. `Config.WriteMetadata` resolves a metadata map
from dependencies per write; `Tools` rejects it when the store lacks the
interface. Metadata is application-visible through `MetadataStore.ReadMetadata`
and never shown to the model. A later write without metadata clears it.

**Scanning.** `Config.Scanner` accepts a `memory.ArtifactScanner`.
`memory.NewScanner` is the built-in stdlib implementation: it matches
common instruction-override phrasing, chat-template role prefixes,
credential-exfiltration commands, HTML comments, and invisible Unicode
controls. Findings are threat/pattern pairs without matched text.

A flagged write is refused as `tool.Failed` and stores nothing. A flagged
read is withheld; a flagged search result carries a notice instead of a
snippet; a flagged `MEMORY.md` is withheld from a snapshot while the file
list remains. Scanning is pattern matching, not a security boundary.

Idempotency is explicitly not added: Golem tools receive no run or call
identity, so a store cannot derive an operation key without new tool
surface. Compare-and-swap remains the guard against lost updates.

## Alternatives considered

- **Require metadata on every Store**: breaks simple backends and forces a
  sidecar or schema decision the port should not make.
- **Let the model supply metadata**: the model could forge provenance.
- **FileStore metadata sidecar**: not atomic with the Markdown and
  editable by file tools; rejected.
- **Full normalization and an extensive scanner ruleset**: valuable but
  larger; the built-in covers common patterns, and the interface lets an
  application supply a stronger scanner.
- **Constructor-time scanning instead of tool-time**: memory changes
  outside the tools, so the trust boundary is read/write time.
- **Idempotency keys in tool arguments**: leaks run identity into the
  model's schema and lets the model choose keys. Deferred until a tool
  call-identity seam exists.

## Consequences

- New exported names: `MetadataStore`, `ArtifactScanner`, `Scanner`,
  `NewScanner`, `Finding`, `Config.WriteMetadata`, and `Config.Scanner`;
  all additive. `File` stays comparable, so no API break.
- `Memory`'s internal entry now carries content and metadata; behavior
  without metadata is unchanged.
- Scanner refusals are model-visible failures; findings do not quote
  content.
- Idempotency remains a documented gap, bounded by CAS and single-writer
  guidance.
- Tests cover metadata recording and store validation, scanner rules,
  refused writes, withheld reads, notices in search, and snapshot
  withholding.
