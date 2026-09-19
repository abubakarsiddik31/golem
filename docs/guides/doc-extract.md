# Document extract

## Purpose

Extract clean, structured Markdown, intact tables, and hierarchical outlines
from Word (.docx), Excel (.xlsx), PowerPoint (.pptx), PDF (.pdf), Markdown (.md),
CSV (.csv), TSV (.tsv), and plain text documents without third-party dependencies,
Python, or an LLM.

## When to use

When an agent needs to inspect, analyze, or summarize office documents,
spreadsheets, presentations, specifications, or datasets in the filesystem.
Use `fileread` for simple text reading without formatting or outlines; use
`docextract` when working with binary office formats (.docx, .xlsx, .pptx) or
when you need outline navigation, sheet filtering, or structured table extraction.

## How it works

`docextract.New[Deps](docextract.Config{Root, ...})` returns an ordinary
`tool.Tool[Deps]` named `extract_doc`. One call:

1. Validates arguments: a missing path, path traversal (`..`), absolute
   path, missing file, or directory rejects the call with `*model.ModelRetry`,
   so the agent's tool retry budget governs correction.
2. Confines paths strictly inside `Root`: symlinks are resolved so a link
   inside the root cannot escape it.
3. Automatically detects format from filename extension or container magic
   bytes.
4. Parses content in pure Go:
   - Word (.docx): extracts headings (H1-H6), bulleted and numbered lists,
     inline styles (bold, italic, strikethrough, monospace), hyperlinks,
     drawings/images, and converts tables to GitHub-Flavored Markdown.
   - Excel (.xlsx): parses sheets, shared strings, numbers, booleans, and
     inline strings into Markdown tables per sheet.
   - PowerPoint (.pptx): reconstructs slide order, slide titles, body shapes,
     tables, and speaker notes.
   - PDF (.pdf): routes seamlessly to `pdfextract` for layout-aware multi-column
     reading order and vector table extraction.
   - Markdown (.md): parses YAML frontmatter, headers, and supports section
     extraction.
   - CSV / TSV: parses delimited tabular records into GFM Markdown tables.
5. Supports outline mode: if `outline: true`, returns only the document's
   structural headings, slide list, or sheet summaries, preserving context
   window budget on large documents.
6. Supports query and page filtering: `query` extracts a specific section or
   filters table rows; `pages` filters slide or sheet indices.
7. Caps text at `MaxBytes` (default 2 MiB) with a truncation marker.

The package also exposes standalone Go APIs `docextract.Extract` and
`docextract.ExtractBytes` for direct use outside agents.

## Example

Run `examples/doc-extract` — offline: a scripted agent extracts an outline from
a Word report, tabular data from an Excel spreadsheet, and a targeted section
from a Markdown document.

```go
extractTool := docextract.MustNew[struct{}](docextract.Config{
    Root: workDir,
})

agent, err := golem.New[struct{}, string](client, decoder,
    golem.WithTools[struct{}, string](extractTool))
```

## API surface

- `docextract.New[Deps](docextract.Config) (tool.Tool[Deps], error)`
- `docextract.MustNew[Deps](docextract.Config) tool.Tool[Deps]`
- `docextract.Extract(context.Context, string, docextract.Options) (*docextract.Document, error)`
- `docextract.ExtractBytes(context.Context, []byte, string, docextract.Options) (*docextract.Document, error)`
- `docextract.DetectFormat([]byte, string) docextract.Format`
- `docextract.Config{Root string, MaxBytes int64, MaxRowsPerSheet int}`
- `docextract.Options{Outline bool, Query string, Pages string, MaxBytes int64, MaxRowsPerSheet int}`
- `docextract.Document`, `docextract.OutlineItem`, `docextract.Format`
- `docextract.ToolName`, `docextract.ToolDescription`
- `docextract.DefaultMaxBytes`

## Gotchas

- The tool's name, description, and argument schema are public contract;
  models and prompts depend on them staying stable.
- The confinement promise is per-read and structural: paths resolve inside
  the root after symlink resolution.
- Outline mode (`outline: true`) should be favored by agents on large documents
  before reading full text to prevent context exhaustion.
- Where common tools live was decided in `docs/adr/0015-common-tools-package.md`.
