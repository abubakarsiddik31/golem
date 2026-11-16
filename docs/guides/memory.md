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
in-process store:

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
- `memory.Memory`, `memory.NewMemory()`
- `memory.File{Path, Content}`
- `memory.ErrNotFound`
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
- Writes are read-modify-write through the toolset's lock, but two
  toolsets sharing one store can race. There is no compare-and-swap or
  idempotency key in v1, so a resumed write may apply twice; scope one
  notebook writer per namespace or serialize in the store.
- `write_memory` refuses to append to or edit a file already over the
  file bound, because it would build from a partial view. Replace or
  delete and rewrite it instead.
- Search is literal and linear; it is not semantic ranking. Use
  [Retrieval](retrieval.md) for embeddings and indexed corpora.
- The deciding design record is `docs/adr/0048-memory-tools.md`.
