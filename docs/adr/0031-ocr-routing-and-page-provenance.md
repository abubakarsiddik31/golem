# ADR 0031: OCR routing, page provenance, and structured OCR tables

## Status

Accepted.

## Context

`pdfextract` runs two parsers over a document: a deterministic text-layer and
vector parser for digital pages, and a pluggable `OCREngine`/`MarkdownRecognizer`
for pages dominated by a raster scan (ADR 0030). Until now the choice between
them was implicit and one-directional:

- Detection was a single heuristic, `IsScannedPage`, gated on
  `Config.DisableOCR`. There was no way to force OCR on a page that ships a
  broken or misleading text layer, nor to pin a stubborn document to the text
  layer when the heuristic misfires.
- Once a page was processed, the `Page` carried no record of *how* it was
  produced. A caller could not tell OCR output from a deterministic parse, so
  downstream trust, caching, and QA decisions had nothing to key on.
- `MarkdownRecognizer` results were Markdown-only. A scanned invoice produced a
  faithful Markdown table, but `Document.Tables` stayed empty, so consumers that
  read structured cells (both the tool's callers and agents) saw scans and
  digital files as different shapes.

The classifier's precision is bounded: it is a 40-character plus image-coverage
heuristic, and it cannot distinguish a genuinely unreadable scan from a page with
a short but usable text layer. Routing must therefore be overridable, and the
outcome must be observable.

## Decision

Make routing explicit, record what happened, and let OCR results carry
structure.

1. **`OCRMode` routing.** Add `Config.Mode`/`Options.Mode` with three values:

   - `ModeAuto` (default, also the empty string) — OCR only pages that look
     scanned, as before.
   - `ModePreferText` — never call the OCR engine, even for pages that look
     scanned.
   - `ModeForceOCR` — run the engine on every page that carries a page image,
     ignoring the text layer. This is the escape hatch for scans whose layer is
     garbage.

   `DisableOCR` remains and wins over every mode; `Mode` is validated at
   construction so a typo fails fast. Detection is decoupled from routing:
   `Page.IsScanned` always reports what the page *looks like*, while `Mode`
   decides what we do with it. `ModeForceOCR` requires a page image to rasterize;
   on an image-less page it falls back to the text layer rather than erroring.

2. **`Page.Source` provenance.** Add a `PageSource` value to every page:

   - `digital` — parsed from a usable text layer, no OCR.
   - `ocr` — produced by the OCR engine (its Markdown replaced the page text).
   - `hybrid` — a span-based engine contributed text *and* the page had digital
     text; both are in the output.
   - `empty` — no extractable text (for example a scan with no engine).

   `Source` reports the outcome, not the intent: a page the classifier called
   scanned but that we chose not to OCR is `empty`, and `IsScanned` stays true.

3. **Structured OCR tables.** Extend `MarkdownResult` with `Tables []Table`.
   `IndraOCREngine` recovers the HTML tables Indra emits in its Markdown and
   returns them as structured `Table` values (rows parsed, entities decoded).
   The extraction loop stamps `Page` onto each table and appends them to
   `Page.Tables`/`Document.Tables` *after* vector layout analysis, so OCR tables
   never perturb reading-order reconstruction that relies on real geometry.

   `Table.BBox` and `Table.Ruled` are left unset for OCR tables: an engine
   reports structure, not vector geometry, and inventing a box would be a lie.
   HTML-table parsing is deliberately a small, focused pass over
   machine-generated `<table><tr><th|td>` markup (standard-library `regexp` and
   `html`), not a general HTML parser.

## Alternatives considered

**A coverage-based classifier (fraction of page area not covered by text).**
More accurate than the character heuristic, but it would not remove the need to
override routing, and it is a strictly riskier change to the hot path. Deferred:
`Mode` and `Source` give callers the control and the observability first.

**A per-region hybrid that OCRs only the rasterised regions of a page.** The
fully general version — segment, IoU-reconcile OCR spans with digital spans,
merge reading orders — is a large design with real regression risk to tables and
reading order. The `hybrid` provenance value reserves the vocabulary, but the
region merge itself is not implemented here.

**Mapping Indra output to `TextSpan`s and running it through the rule pipeline.**
Attractive for uniformity, but Indra's value is precisely the layout it already
resolved: routing its output through Golem's span pipeline would discard the
tables and reading order it recovered (a scan has no vector rects for the
lattice detector to find), a regression traded for cosmetic consistency.

**Heuristic figure cropping from scans.** Using OCR text boxes to infer figure
regions was prototyped and found unreliable; it is not shipped. The `Page.Source`
vocabulary and structured `Tables` cover the cases that are trustworthy today.

**A separate OCR batch API.** Unchanged from ADR 0030: the engine seam is per
page and the parser owns layout.

## Consequences

Small, additive public surface to document and freeze: `OCRMode` with
`ModeAuto`/`ModePreferText`/`ModeForceOCR`, `PageSource` with
`PageSourceDigital`/`PageSourceOCR`/`PageSourceHybrid`/`PageSourceEmpty`,
`Page.Source`, `MarkdownResult.Tables`, and the `Mode` field on `Config` and
`Options`. All zero-value compatible: an existing caller that sets nothing keeps
`ModeAuto` behavior.

Scanned pages now populate `Document.Tables` like digital pages, so consumers can
read cells uniformly. The trade-off is a narrow HTML-table parser in the Indra
path; it is bounded to Indra's own output and covered by contract tests.

Two capabilities are explicitly deferred and recorded here so they are decisions
rather than oversights: a coverage-based classifier, and per-region hybrid OCR.
Both are additive to this model (`Source` already reserves `hybrid`).
