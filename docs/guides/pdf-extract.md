# PDF extract

## Purpose

Extract structured text, intact tables, multi-column reading order, and image
placeholders with descriptions from PDF files without Python, Tesseract, or an
LLM.

## When to use

When an agent needs to inspect and analyze documents, financial sheets, or
academic papers in PDF format. Use `fileread` instead for plain text files
(JSON, Markdown, source code); use `pdfextract` when the file is a PDF whose
columns, tables, and figures must be preserved as structured Markdown.

## How it works

`pdfextract.New[Deps](pdfextract.Config{Root, ...})` returns an ordinary
`tool.Tool[Deps]` named `extract_pdf` whose required argument is a `path`
string relative to `Root`, with an optional `pages` filter. One call:

1. Validates the arguments: a missing path, path traversal (`..`), absolute
   path, missing file, or directory rejects the call with `*model.ModelRetry`,
   so the agent's tool retry budget governs correction.
2. Resolves the path strictly inside `Root`: symlinks are resolved so a link
   inside the root cannot escape it.
3. Parses the PDF structure, resolving cross-reference tables, stream
   filters, font dictionaries, and content stream operators.
4. Triages pages: digital vector pages are parsed in microseconds (under
   0.1ms per page). Pages dominated by raster images are routed to the
   configured modern OCR engine.
5. Reconstructs multi-column reading order using the Recursive XY-Cut
   algorithm, guaranteeing column 1 is read top-to-bottom before column 2.
6. Extracts intact tables:
   - Ruled tables (Lattice) are detected from vector path strokes,
     reconstructing exact cell coordinates and emitting GitHub-Flavored
     Markdown tables.
   - Borderless tables (Stream) are detected via recurring column whitespace
     gutters.
7. Matches embedded images with adjacent author captions using spatial
   proximity search, emitting Markdown placeholders like `![Figure 1:
   Caption](images/page_1_img_1.png)`. If `ReturnImageParts` is true, images
   are returned as `model.Part` values per ADR 0025.
8. Caps text at `MaxBytes` (default 2 MiB) with a truncation marker.

The package also exposes standalone Go APIs `pdfextract.Extract` and
`pdfextract.ExtractBytes` for direct use outside agents.

## Example

Run `examples/pdf-extract` — offline: a scripted agent extracts a multi-column
report with an intact table and an image placeholder.

```go
extract := pdfextract.MustNew[struct{}](pdfextract.Config{
    Root: workDir,
})

agent, err := golem.New[struct{}, string](client, decoder,
    golem.WithTools[struct{}, string](extract))
```

## API surface

- `pdfextract.New[Deps](pdfextract.Config) (tool.Tool[Deps], error)`
- `pdfextract.MustNew[Deps](pdfextract.Config) tool.Tool[Deps]`
- `pdfextract.Extract(context.Context, string, pdfextract.Options) (*pdfextract.Document, error)`
- `pdfextract.ExtractBytes(context.Context, []byte, pdfextract.Options) (*pdfextract.Document, error)`
- `pdfextract.Config{Root string, MaxBytes int64, ImageDir string, ReturnImageParts bool, OCREngine OCREngine, DisableOCR bool}`
- `pdfextract.Options{Pages string, ImageDir string, ReturnImageParts bool, OCREngine OCREngine, DisableOCR bool, MaxBytes int64}`
- `pdfextract.Document`, `pdfextract.Page`, `pdfextract.Table`, `pdfextract.ImageRef`, `pdfextract.LayoutBlock`
- `pdfextract.ToolName`, `pdfextract.ToolDescription`
- `pdfextract.DefaultMaxBytes`
- `pdfextract.OCREngine`

## Gotchas

- The tool's name, description, and argument schema are public contract;
  models and prompts depend on them staying stable.
- The confinement promise is per-read and structural: paths resolve inside
  the root after symlink resolution.
- Digital vector pages extract with 100% character accuracy from the embedded
  text stream; scanned raster documents require an OCR engine.
- Where common tools live was decided in `docs/adr/0015-common-tools-package.md`.
