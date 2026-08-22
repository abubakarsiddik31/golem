# File read

## Purpose

Let a model read the files it is working with: the `fileread` package
builds a tool that returns one file's text content from inside a
configured root directory, with every read confined to that root.

## When to use

Coding and analysis agents that must inspect source, notes, or data
files the application already has on disk. Not for files the
application can read itself and hand the model as a dependency — when
the caller controls the read, a typed tool with typed arguments beats
handing the model a filesystem. Not for writing; this tool is
read-only.

## How it works

`fileread.New[Deps](fileread.Config{Root, MaxBytes})` returns an
ordinary `tool.Tool[Deps]` named `read_file` whose single required
argument is a `path` string relative to `Root`. One call:

1. Validates the arguments — a missing, non-string, or empty `path`
   rejects the call with `*model.ModelRetry`, so the agent's tool
   retry budget governs correction.
2. Resolves the path strictly inside `Root`: absolute paths and any
   `..` segment reject as correctable mistakes, and symlinks are
   resolved so a link inside the root cannot read a file outside it.
   `Root` itself must exist and be a directory at construction.
3. Requires a regular file: a missing path or a directory rejects as
   correctable — the model can try another path.
4. Reads up to `MaxBytes` (default 1 MiB). A longer file is truncated,
   not failed; the result ends with a `[fileread: file truncated at N
   bytes]` marker.
5. Returns the content only when the sniffed media type is text-like
   (any `text/*` type, JSON, XML, YAML, JavaScript); binary files fail
   at the tool stage with `*fileread.UnsupportedContentError`.

Filesystem errors other than missing files — permission failures,
unreadable roots — fail at the tool stage with the source error
preserved; they are never correctable rejections.

## Example

Run `examples/file-read` — offline: a temp directory stands in for the
workspace and a scripted fake model requests the read.

```go
read := fileread.MustNew[struct{}](fileread.Config{Root: workDir})

agent, err := golem.New[struct{}, string](client, decoder,
    golem.WithTools[struct{}, string](read))
```

## API surface

- `fileread.New[Deps](fileread.Config) (tool.Tool[Deps], error)` / `fileread.MustNew[Deps](fileread.Config) tool.Tool[Deps]`
- `fileread.Config{Root string, MaxBytes int64}`
- `fileread.ToolName`, `fileread.ToolDescription` — the tool's stable identity
- `fileread.UnsupportedContentError{Path string, ContentType string}`
- `fileread.DefaultMaxBytes`

## Gotchas

- The tool's name, description, and argument schema are public
  contract; models and prompts depend on them staying stable.
- The confinement promise is per-read and structural: paths resolve
  inside the root after symlink resolution. The tool cannot grant
  access the process lacks — it reads with the process's own
  permissions.
- Content typing is sniffed from the first bytes, not taken from a
  file extension; a text file with a misleading extension still reads.
- Files are assumed UTF-8; no transcoding (the package is
  standard-library only).
- The package depends only on the standard library, `model`, and
  `tool` — never on the root package or a provider.
- Where common tools live was decided in
  `docs/adr/0015-common-tools-package.md`.
