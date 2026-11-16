# ADR 0057: File-backed memory with compare-and-swap

Date: 2026-11-16
Status: Accepted

## Context

ADR 0048 shipped memory behind a four-method `Store` port with an
in-process implementation. Applications can implement durable backends,
but the package offered no file implementation, and write tools were
last-write-wins: two concurrent writers, or a resumed run, could
overwrite an edit silently.

Pydantic AI Harness ships `FileMemoryStore` and compare-and-swap writes.
Golem's constraints allow both without a dependency: Markdown files,
`os.Rename`, and a content hash.

## Decision

`memory.FileStore` stores each namespace as a directory tree under a
root, one file per notebook path. Writes go to a temporary file in the
destination directory and are renamed into place; files are created
0600. Reads resolve symlinks and reject any path escaping the root;
writes resolve the destination directory before creating the temp file.
Listings skip symlinks.

`File` gains a `Version` field, a SHA-256 hash of content. `VersionedStore`
is an optional extension of `Store`:

```go
WriteVersioned(ctx, namespace, path, content, expectedVersion) error
DeleteVersioned(ctx, namespace, path, expectedVersion) error
```

An empty expected version is create-only; a non-empty one must match the
current content. Mismatches return `ErrVersionConflict`, which the tools
turn into `&tool.Failed` asking the model to read again. `Memory` also
implements `VersionedStore` under its existing mutex. FileStore locks per
instance, not across processes.

## Alternatives considered

- **Change `Store` to require versions**: breaks existing implementations
  and makes a simple backend carry concurrency policy. The optional
  interface is additive.
- **Expose versions in tool arguments**: the model would manage internal
  tokens and could forge them. Tools read the version themselves.
- **Idempotency receipts**: Golem tools receive no run or call identity,
  so a store cannot derive an operation key without new tool surface.
  Deferred; the guide documents that a resumed write may apply twice.
- **Write metadata in a sidecar**: it cannot change atomically with the
  Markdown and the model's file tools could edit it. FileStore stays
  metadata-free.
- **A database store in the package**: violates the application-owned
  backend rule; the port is the extension point.

## Consequences

- New exported names: `FileStore`, `NewFileStore`, `VersionedStore`,
  `ErrVersionConflict`, and `File.Version`; all additive.
- Tools use compare-and-swap when the store supports it and keep the
  existing behavior otherwise.
- FileStore's per-instance lock is documented; concurrent processes need
  application-level serialization.
- Symlink escape and traversal tests pin the filesystem boundary.
- Artifact scanning and write metadata remain follow-up work.
