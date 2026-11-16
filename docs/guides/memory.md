# Memory

## Purpose

Give an agent a persistent Markdown notebook it reads, searches, and
edits through tools, so facts learned in one run are available in a
later one without loading the whole notebook into every prompt.

## When to use

Long-lived assistants that should remember user preferences, project
facts, or decisions across conversations. For the conversation itself,
use [Sessions](sessions.md); for searching a document corpus, use
[Retrieval](retrieval.md). Memory is application-owned state, not chat
history and not a vector index.

## How it works

The notebook is a set of Markdown files per namespace. `MEMORY.md` is
the main file; other files hold focused notes. The package returns five
ordinary tools:

- `list_memory` lists the notebook's paths.
- `read_memory` reads one file, truncating a large file with a marker.
- `write_memory` appends (the default), replaces the whole file, or
  edits one unique `oldText` fragment.
- `search_memory` performs bounded literal search and returns matching
  paths with snippets.
- `delete_memory` deletes one file; `MEMORY.md` is protected.

The notebook lives behind `memory.Store`: `List`, `Read`, `Write`, and
`Delete`, each taking a namespace. `Config.Namespace` resolves the
namespace from the run's dependencies, so one agent can serve many users
without the model selecting a namespace in a tool call. A nil
`Namespace` uses one empty namespace. `memory.Memory` is the in-process
implementation; durable stores stay application-side behind the port.

`memory.FileStore` is the file-backed implementation: one Markdown tree
per namespace under a root directory, atomic writes, and a SHA-256
content version. It also implements `memory.VersionedStore`, so the
tools compare the version they read against the current file before a
write or delete; a concurrent edit becomes a correctable
`tool.Failed` ("changed concurrently") rather than a lost update. An
empty expected version is create-only. `FileStore` skips symlinks in
listings and rejects reads or writes that resolve outside its root. Its
lock is per instance: concurrent processes sharing one directory still
need application-level serialization.

### Write metadata

`memory.MetadataStore` is an optional extension of `VersionedStore`:
`WriteVersionedWithMetadata` records an application-supplied metadata map
in the same atomic step as the content. `memory.Memory` implements it;
`FileStore` does not, because a sidecar could not change atomically with
the Markdown.

```go
tools, _ := memory.Tools(memory.Config[user]{
    Store: memory.NewMemory(),
    WriteMetadata: func(u user) map[string]string {
        return map[string]string{"updated_by": "agent", "user_id": u.ID}
    },
})
```

Metadata reaches the application through
`MetadataStore.ReadMetadata`; it is never shown to the model, and a later write without
`WriteMetadata` clears it. `Tools` rejects a store that does not
implement `MetadataStore` when `WriteMetadata` is set.

### Artifact scanning

`Config.Scanner` accepts a `memory.ArtifactScanner`; `memory.NewScanner`
is the built-in dependency-free implementation. It flags common
prompt-injection phrasing, chat-template role overrides,
credential-exfiltration commands, hidden HTML comments, and invisible
Unicode controls. Findings name threats without quoting the matched text.

```go
memory.Tools(memory.Config[user]{
    Store:   memory.NewMemory(),
    Scanner: memory.NewScanner(),
})
```

A flagged write is refused with `tool.Failed` and stores nothing. A
flagged read is withheld, a flagged search result shows a notice instead
of its snippet, and a flagged `MEMORY.md` is withheld from a snapshot
while the file list stays visible. Pattern matching is one layer, not a
security boundary; combine it with namespace isolation and your own
guards.

Paths and namespaces are validated before they reach the store:
relative, `/`-separated segments of letters, digits, `.`, `_`, and `-`,
with no `.` or `..` segments. The default file bound is 64 KiB, search
returns at most 10 results from at most 1,000 files, and combined result
text is capped at 4,000 characters.

Bad model input — an invalid path, a missing file, a conflicting edit,
or a write over the file bound — returns `&tool.Failed`, so the model
can correct the call. A store failure is an infrastructure error and
aborts the run at the tool stage.

By default the model uses the tools on demand. Automatic recall is
opt-in: `memory.Snapshot` reads a bounded excerpt of `MEMORY.md` and the
other file names, and the application returns it from
`golem.WithPromptContext`, which places it after history and before the
new prompt as a user-role turn. The snapshot frames stored text as data
from earlier runs, not instructions; it is evaluated once per run and is
kept in the result evidence. An empty notebook contributes nothing.

### Automatic recall

```go
agent, _ := golem.New[user, string](client, decoder,
	golem.WithPromptContext[user, string](func(ctx context.Context, runCtx golem.RunContext[user]) ([]model.Message, error) {
		message, ok, err := memory.Snapshot(ctx, cfg, runCtx.Deps)
		if err != nil || !ok {
			return nil, err
		}
		return []model.Message{message}, nil
	}),
)
```

`Snapshot` is bounded by `MaxSnapshotChars` (default 8,000) and
`MaxSnapshotLines` (default 200); a cut excerpt points the model at
`read_memory`. The caller decides whether a store failure should fail
the run or proceed without memory.

## Example

`examples/memory` runs offline against a scripted model and the
in-process store. `examples/memory-files` uses the file store and shows a
stale edit being rejected:

```go
tools, err := memory.Tools(memory.Config[user]{
	Store:     memory.NewMemory(),
	Namespace: func(u user) string { return u.ID },
})
agent, err := golem.New[user, string](client, decoder,
	golem.WithInstructions[user, string](memory.Instructions),
	golem.WithTools[user, string](tools...),
)
```

## API surface

- `memory.Tools[Deps](memory.Config[Deps]) ([]tool.Tool[Deps], error)`
- `memory.Config[Deps]{Store, Namespace func(Deps) string, MaxFileBytes, MaxResults, MaxResultChars, MaxSearchFiles, MaxSnapshotChars, MaxSnapshotLines}`
- `memory.Store` — `List`, `Read`, `Write`, `Delete`
- `memory.VersionedStore` — optional `WriteVersioned`/`DeleteVersioned` compare-and-swap
- `memory.MetadataStore` — optional metadata-atomic write; `Config.WriteMetadata`
- `memory.ArtifactScanner`, `memory.Scanner`, `memory.NewScanner()`, `memory.Finding`
- `memory.Config.Scanner`
- `memory.Memory`, `memory.NewMemory()`
- `memory.FileStore`, `memory.NewFileStore(root)`
- `memory.File{Path, Content, Version}`
- `memory.ErrNotFound`, `memory.ErrVersionConflict`
- `memory.Snapshot[Deps](ctx, cfg, deps) (model.Message, bool, error)`
- `memory.MainFile`, `memory.Instructions`
- `golem.WithPromptContext[Deps, Output]` — the per-run user-role seam
- Constants: `DefaultMaxFileBytes`, `DefaultMaxResults`, `MaxResultsLimit`, `DefaultMaxResultChars`, `DefaultMaxSearchFiles`

## Gotchas

- The notebook is model-written and persists across runs. Treat it as
  untrusted data, not instructions; memory can carry injected text from
  an earlier prompt. Read only what the task needs, and pair memory with
  your own input/tool guards. A snapshot is user-role context, not a
  system prompt; it lowers authority but is not a hard injection boundary.
- Automatic recall is explicit: `WithPromptContext` runs once per run,
  so a snapshot is added again on each run that returns it. Keep the
  snapshot small and decide whether repeated recall is acceptable for
  your conversation model.
- Namespaces select records; they are not authorization. Authenticate
  the caller in application dependencies and scope the store's
  credentials.
- Writes are read-modify-write through the toolset's lock. A
  `VersionedStore` adds compare-and-swap, but two toolsets sharing a
  non-versioned store can still race. There is no idempotency key in the
  tool contract, so a resumed write may apply twice; scope one notebook
  writer per namespace or serialize in the store.
- `FileStore` locks per instance, not across processes. Use one instance
  per root, or a database store for concurrent writers.
- `FileStore` does not store write metadata; a sidecar could not change
  atomically with the Markdown. Metadata and artifact scanning remain
  backend or application concerns.
- `write_memory` refuses to append to or edit a file already over the
  file bound, because it would build from a partial view. Replace or
  delete and rewrite it instead.
- Search is literal and linear; it is not semantic ranking. Use
  [Retrieval](retrieval.md) for embeddings and indexed corpora.
- The deciding design record is `docs/adr/0048-memory-tools.md`.
