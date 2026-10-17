# ADR 0030: Local subprocess OCR engine

## Status

Accepted.

## Context

`pdfextract` triages every page and, for pages dominated by a raster scan,
hands the page image to a pluggable `OCREngine` (ADR 0025 covers the
multimodal-parts alternative). Two engines ship today: `ModelOCREngine`,
which calls a vision model, and `MistralOCREngine`, which calls Mistral's
hosted API. Both are accurate, but the vision-model path costs tokens and
the Mistral path is a paid network call. Users who need to OCR scanned
PDFs locally, offline, and cheaply have no first-class option.

The best available CPU OCR models — Indra (`datalab-to/indra`) among them —
are distributed as Python packages over ONNX Runtime, with no Go bindings.
Golem's core promise is zero external dependencies and no cgo, so embedding
such a runtime is out of the question: it would add a C++ shared library,
break cross-compilation, and put a native ML runtime in the module's
dependency graph.

## Decision

Ship `IndraOCREngine`, an `OCREngine`/`MarkdownRecognizer` that shells out to
the external `indra-ocr` command-line tool through `os/exec`. It:

- adds no Go dependency (standard library `os/exec` only) and no cgo;
- returns page Markdown directly, so it plugs into the existing
  `MarkdownRecognizer` fast path with no new concept in the extraction loop;
- treats the subprocess as an injectable seam: `IndraOCRConfig.Run` defaults
  to `exec.CommandContext` and can be replaced, so contract tests run offline
  with no binary, no model download, and no network;
- fails fast at construction when the binary is absent from `PATH`, and
  bounds every call with a context deadline that kills the process on
  cancellation;
- wraps every failure with `pdfextract: indra ocr:` and preserves the cause
  for `errors.Is`/`errors.As`.

Model weights stay the user's responsibility: the engine downloads nothing
and ships nothing. This is also the one place the licensing boundary is
visible — Indra's code is Apache-2.0, but its weights are OpenRAIL-M, so the
engine documents that the caller installs and licenses the tool.

## Alternatives considered

**Embedding an ONNX runtime.** A pure-Go runtime does not exist at usable
maturity; `purego`-based bindings still require the native ONNX Runtime
library, and pure-Go interpreters have incomplete operator coverage. Any of
these violates the zero-dependency rule and the Windows cross-compile.

**Porting Indra's pipeline to Go.** Requires reimplementing ONNX inference
*and* a PDF rasterizer (Go has no PDF renderer) — months of work to duplicate
a model we cannot redistribute, against everything the project optimizes for.

**A hosted OCR API (Mistral-style).** Already shipped as `MistralOCREngine`;
it does not satisfy the offline/local requirement and adds a paid dependency.

**A new document-level batch API.** Indra's headline throughput comes from
processing a whole PDF in one multicore process, but Golem's engine seam is
per page and its own parser already owns layout and reading order. Widening
the seam for a faster batch would duplicate the extractor for a modest gain;
the per-page CLI call is kept, and its cost is documented.

## Consequences

One more small public surface to document and freeze: `IndraOCREngine`,
`IndraOCRConfig`, `NewIndraOCR`, `MustNewIndraOCR`, and the two defaults.
Per-page subprocess startup is paid on every scanned page; the guide states
this and the fix (a future batch path) is left for demand. The engine is
optional and imported only by applications that want it, so applications
that never OCR scans see no cost — the same adoption model as the other
common tools (ADR 0015).
