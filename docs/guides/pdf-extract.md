# PDF extract

## Purpose

Extract structured text, intact tables, multi-column reading order, and image
placeholders with descriptions from PDF files without Python, Tesseract, or an
LLM, with generalized support for scanned page analysis via multimodal parts,
small vision models (Gemini Flash-Lite, local Ollama), or Mistral OCR with
cost tracking.

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
   0.1ms per page). Pages dominated by raster scans are identified and
   analyzed according to the configured strategy:
   - **Agent Multimodal Parts (`ReturnScannedPageParts: true`):** The tool
     extracts the page raster image and attaches it directly as a `model.Part`
     in `tool.Result.Parts` per ADR 0025. Multimodal agents (Claude, GPT-4o,
     Gemini) inspect the image directly.
   - **Small Vision Models (`ModelOCREngine`):** A fast, low-cost vision
     model (e.g. `gemini-2.5-flash-lite`, `gemini-3.1-flash-lite`, or local
     Ollama `qwen2.5-vl:3b`) transcribes the scanned page directly into
     clean GitHub-Flavored Markdown. Token usage and USD cost are tracked on
     `Page.Usage`/`Page.Cost` and `Document.Usage`/`Document.Cost`.
   - **Dedicated Mistral OCR (`MistralOCREngine`):** Calls Mistral's
     `mistral-ocr-latest` API over stdlib HTTP, extracting structured markdown
     and tracking per-page cost.
   - **Local Indra OCR (`IndraOCREngine`):** Shells out to the external
     `indra-ocr` CLI ([datalab-to/indra](https://github.com/datalab-to/indra))
     to OCR scanned pages on the host CPU — offline, no API key, no Go
     dependency, and no model shipped with Golem.
   - **Pluggable Local/External OCR:** Implements `OCREngine` returning
     spatial `TextSpan`s.
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
   Caption](images/page_1_img_1.png)`. Full-page background scans are
   distinguished from embedded figures (`page.IsScanned` and `page.ScanImage`)
   and do not emit false figure placeholders.
8. Caps text at `MaxBytes` (default 2 MiB) with a truncation marker.

The package also exposes standalone Go APIs `pdfextract.Extract` and
`pdfextract.ExtractBytes` for direct use outside agents.

## Example

Run `examples/pdf-extract` — offline: a scripted agent extracts a multi-column
report with an intact table and an image placeholder, as well as a scanned page.

```go
extract := pdfextract.MustNew[struct{}](pdfextract.Config{
    Root:                   workDir,
    ReturnScannedPageParts: true,
})

agent, err := golem.New[struct{}, string](client, decoder,
    golem.WithTools[struct{}, string](extract))
```

### Using Model-Backed OCR with Gemini Flash-Lite & Cost Tracking

```go
geminiClient, _ := gemini.New(gemini.Config{
    APIKey: os.Getenv("GEMINI_API_KEY"),
    Model:  "gemini-2.5-flash-lite",
})

ocrEngine := pdfextract.NewModelOCR(geminiClient,
    pdfextract.WithModelPrice(gemini.Price{
        InputPerMTok:  0.10,
        OutputPerMTok: 0.40,
    }),
)

doc, err := pdfextract.Extract(ctx, "scanned_doc.pdf", pdfextract.Options{
    OCREngine: ocrEngine,
})
fmt.Printf("Transcribed %d pages, cost: $%.5f\n", doc.NumPages, doc.Cost)
```

### Using Local Models (Ollama)

```go
localClient, _ := openai.New(openai.Config{
    BaseURL: "http://localhost:11434/v1",
    Model:   "qwen2.5-vl:3b",
})

ocrEngine := pdfextract.NewModelOCR(localClient) // $0.00 local compute cost
```

### Using Mistral OCR

```go
mistralOCR, err := pdfextract.NewMistralOCR(pdfextract.MistralOCRConfig{
    APIKey:       os.Getenv("MISTRAL_API_KEY"),
    PricePerPage: 0.002, // $2 per 1,000 pages
})
```

### Using Local Indra OCR

`IndraOCREngine` shells out to the `indra-ocr` CLI
([datalab-to/indra](https://github.com/datalab-to/indra)) to OCR scanned pages
on the host CPU. It is offline, needs no API key, and adds no Go dependency —
Golem ships no model and downloads nothing; the CLI and its weights are the
caller's to install (`pip install indra-ocr`). `New` fails fast when the binary
is not on `PATH`.

```go
ocrEngine, err := pdfextract.NewIndraOCR(pdfextract.IndraOCRConfig{
    Cores:         4,     // limit OCR worker processes
    KeepFurniture: false, // drop page headers/footers (default)
})

doc, err := pdfextract.Extract(ctx, "scanned_doc.pdf", pdfextract.Options{
    OCREngine: ocrEngine,
})
fmt.Println(doc.Markdown) // structured Markdown, no API cost
```

`IndraOCRConfig.Run` replaces the subprocess call; leave it nil in production
and set it to fake OCR execution in tests (see `examples/ocr-scanned-pdf`).

## API surface

- `pdfextract.New[Deps](pdfextract.Config) (tool.Tool[Deps], error)`
- `pdfextract.MustNew[Deps](pdfextract.Config) tool.Tool[Deps]`
- `pdfextract.Extract(context.Context, string, pdfextract.Options) (*pdfextract.Document, error)`
- `pdfextract.ExtractBytes(context.Context, []byte, pdfextract.Options) (*pdfextract.Document, error)`
- `pdfextract.Config{Root string, MaxBytes int64, ImageDir string, ReturnImageParts bool, ReturnScannedPageParts bool, OCREngine OCREngine, DisableOCR bool}`
- `pdfextract.Options{Pages string, ImageDir string, ReturnImageParts bool, ReturnScannedPageParts bool, OCREngine OCREngine, DisableOCR bool, MaxBytes int64}`
- `pdfextract.Document{NumPages int, Pages []Page, Markdown string, Tables []Table, Images []ImageRef, Usage model.Usage, Cost float64}`
- `pdfextract.Page{Index int, Width float64, Height float64, IsScanned bool, ScanImage *ImageRef, Markdown string, Tables []Table, Images []ImageRef, Blocks []LayoutBlock, Usage model.Usage, Cost float64}`
- `pdfextract.NewModelOCR(model.Model, ...ModelOCROption) (*ModelOCREngine, error)`
- `pdfextract.MustNewModelOCR(model.Model, ...ModelOCROption) *ModelOCREngine`
- `pdfextract.WithModelPrice(model.Price) ModelOCROption`
- `pdfextract.WithModelPrompt(string) ModelOCROption`
- `pdfextract.NewMistralOCR(MistralOCRConfig) (*MistralOCREngine, error)`
- `pdfextract.MustNewMistralOCR(MistralOCRConfig) *MistralOCREngine`
- `pdfextract.MistralOCRConfig{APIKey string, BaseURL string, Model string, PricePerPage float64, HTTPClient *http.Client}`
- `pdfextract.NewIndraOCR(IndraOCRConfig) (*IndraOCREngine, error)`
- `pdfextract.MustNewIndraOCR(IndraOCRConfig) *IndraOCREngine`
- `pdfextract.IndraOCRConfig{BinaryPath string, ModelDir string, DPI int, Cores int, KeepFurniture bool, Timeout time.Duration, Run func(context.Context, string, []string) ([]byte, error)}`
- `pdfextract.DefaultIndraOCRBin`, `pdfextract.DefaultIndraOCRTimeout`
- `pdfextract.MarkdownRecognizer`, `pdfextract.MarkdownResult`
- `pdfextract.ToolName`, `pdfextract.ToolDescription`
- `pdfextract.DefaultMaxBytes`
- `pdfextract.OCREngine`

## Gotchas

- The tool's name, description, and argument schema are public contract;
  models and prompts depend on them staying stable.
- The confinement promise is per-read and structural: paths resolve inside
  the root after symlink resolution.
- Digital vector pages extract with 100% character accuracy from the embedded
  text stream; scanned raster documents require an OCR engine or `ReturnScannedPageParts`.
- When using `ModelOCREngine`, small vision models (`gemini-2.5-flash-lite`, `qwen2.5-vl:3b`)
  are recommended for speed, cost efficiency, and low latency.
- `IndraOCREngine` is not selected by `DefaultOCREngine`; pass it explicitly as
  `Options.OCREngine`/`Config.OCREngine`. It invokes the CLI once per scanned
  page, so per-page process startup is paid on every page — for very large scans
  prefer a future batch path. The Indra model weights are OpenRAIL-M; review
  their terms (they are free for research, personal use, and small startups)
  before commercial deployment.
- Where common tools live was decided in `docs/adr/0015-common-tools-package.md`.
