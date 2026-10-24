// Package pdfextract provides a high-performance, layout-aware PDF
// extraction tool and library for Golem. It reconstructs reading order,
// extracts intact markdown tables (ruled and borderless), places image
// placeholders with author caption proximity matching, and detects
// scanned pages for modern deep-learning OCR — without Python, without
// Tesseract, and without an LLM.
//
// The tool is an ordinary tool.Tool[Deps]: it composes with any agent
// dependency type and every agent option. Its name, description, and
// argument schema are public contract — models and prompts depend on
// them staying stable.
package pdfextract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// ToolName is the name of the tool New returns.
const ToolName = "extract_pdf"

// ToolDescription is the description the model sees for the tool.
const ToolDescription = "Extract structured text, intact markdown tables, layout-aware reading order, and image placeholders with descriptions from a PDF file. Fast, local, and LLM-free."

// DefaultMaxBytes bounds extraction text output when Config.MaxBytes is zero.
const DefaultMaxBytes int64 = 2 << 20 // 2 MiB

// schema describes the tool's arguments; it is public contract.
var schema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "File path relative to the root directory, without .. segments."
		},
		"pages": {
			"type": "string",
			"description": "Optional page range to extract (e.g. '1-3', '5', or 'all'). Defaults to 'all'."
		}
	},
	"required": ["path"],
	"additionalProperties": false
}`)

// Config configures the PDF extraction tool. Root is required; other fields are optional.
type Config struct {
	// Root is the directory every extraction read is confined to. It must exist
	// and be a directory; symlinks in it are resolved at construction.
	Root string

	// MaxBytes caps the returned text output; longer output is truncated with
	// a truncation marker. Zero selects DefaultMaxBytes; negative values fail New.
	MaxBytes int64

	// ImageDir is an optional directory where extracted images are saved.
	// If empty, images are referenced by virtual paths and kept in memory.
	ImageDir string

	// ReturnImageParts determines whether extracted images are returned as
	// model.Part (image parts) in tool.Result per ADR 0025.
	ReturnImageParts bool

	// ReturnScannedPageParts determines whether scanned page images are returned
	// as model.Part in tool.Result for direct visual analysis by multimodal models.
	ReturnScannedPageParts bool

	// OCREngine provides the modern deep-learning OCR implementation for scanned pages.
	// Nil disables OCR: scanned pages stay PageSourceEmpty rather than receiving
	// placeholder text. Set IndraOCREngine or ModelOCREngine to enable it.
	OCREngine OCREngine

	// Mode selects how pages are routed between the text layer and OCREngine.
	// Empty or ModeAuto OCRs only pages whose text layer is missing or unusable;
	// ModePreferText never OCRs; ModeForceOCR always OCRs pages with an image.
	// DisableOCR, when true, wins over every mode.
	Mode OCRMode

	// DisableOCR skips OCR even on scanned pages.
	DisableOCR bool
}

// Options configures a direct extraction call (outside an agent).
type Options struct {
	Pages                  string // "all", "1-3", "5"
	ImageDir               string
	ReturnImageParts       bool
	ReturnScannedPageParts bool
	OCREngine              OCREngine
	Mode                   OCRMode // "" == ModeAuto
	DisableOCR             bool
	MaxBytes               int64
}

// PageSource reports how a page's content was produced, so callers can tell a
// deterministic text-layer parse apart from OCR output.
type PageSource string

const (
	// PageSourceDigital is a page parsed from a usable embedded text layer.
	PageSourceDigital PageSource = "digital"
	// PageSourceOCR is a page transcribed entirely by the configured OCR engine.
	PageSourceOCR PageSource = "ocr"
	// PageSourceHybrid is a page where both a text layer and OCR contributed.
	PageSourceHybrid PageSource = "hybrid"
	// PageSourceEmpty is a page with no extractable text (for example a scan with
	// no OCR engine configured).
	PageSourceEmpty PageSource = "empty"
)

// OCRMode selects how each page is routed between the deterministic text-layer
// parser and the configured OCR engine.
type OCRMode string

const (
	// ModeAuto OCRs only pages whose text layer is missing or unusable. This is
	// the default when Mode is empty.
	ModeAuto OCRMode = "auto"
	// ModePreferText always parses the text layer and never calls the OCR engine,
	// even when the page looks scanned.
	ModePreferText OCRMode = "prefer-text"
	// ModeForceOCR runs the OCR engine on every page that carries a page image,
	// ignoring the text layer. It is the escape hatch for scans that ship a
	// low-quality or misleading text layer.
	ModeForceOCR OCRMode = "force-ocr"
)

// Valid reports whether the mode is one of the recognised values (or empty,
// which means ModeAuto).
func (m OCRMode) Valid() bool {
	switch m {
	case "", ModeAuto, ModePreferText, ModeForceOCR:
		return true
	default:
		return false
	}
}

// Page holds extracted elements and Markdown text for one page.
type Page struct {
	Index     int
	Width     float64
	Height    float64
	IsScanned bool       // true if the page was detected as a scanned raster document
	Source    PageSource // how this page's content was produced
	ScanImage *ImageRef  // primary scanned page image, when IsScanned is true
	Markdown  string
	Tables    []Table
	Images    []ImageRef
	Blocks    []LayoutBlock
	Usage     model.Usage // token usage incurred for this page (e.g. from ModelOCREngine)
	Cost      float64     // monetary cost incurred for this page in US dollars
	// Err is non-nil when the page could not be extracted. The page still
	// appears in Document.Pages so page counts stay accurate.
	Err error
}

// Document represents a fully extracted PDF document.
type Document struct {
	NumPages int
	Pages    []Page
	Markdown string
	Tables   []Table
	Images   []ImageRef
	Usage    model.Usage // total token usage incurred across all OCR calls
	Cost     float64     // total monetary cost incurred across all OCR calls in US dollars
}

// UnsupportedFileError reports a file that is not a valid PDF.
type UnsupportedFileError struct {
	Path   string
	Reason string
	// Err is the underlying parse error, when there is one, so callers can use
	// errors.Is for sentinels such as ErrEncrypted.
	Err error
}

func (e *UnsupportedFileError) Error() string {
	return fmt.Sprintf("pdfextract: %s: invalid PDF file: %s", e.Path, e.Reason)
}

// Unwrap exposes the underlying parse error to errors.Is and errors.As.
func (e *UnsupportedFileError) Unwrap() error { return e.Err }

// extractor holds resolved configuration for tool execution.
type extractor struct {
	root                   string
	maxBytes               int64
	imageDir               string
	returnImageParts       bool
	returnScannedPageParts bool
	ocrEngine              OCREngine
	mode                   OCRMode
	disableOCR             bool
}

// New validates cfg and returns the extract_pdf tool ready for registration with an agent.
func New[Deps any](cfg Config) (tool.Tool[Deps], error) {
	if cfg.MaxBytes < 0 {
		return tool.Tool[Deps]{}, fmt.Errorf("pdfextract: max bytes must not be negative, got %d", cfg.MaxBytes)
	}
	if cfg.Root == "" {
		return tool.Tool[Deps]{}, fmt.Errorf("pdfextract: root is required")
	}
	root, err := filepath.EvalSymlinks(cfg.Root)
	if err != nil {
		return tool.Tool[Deps]{}, fmt.Errorf("pdfextract: root %s: %w", cfg.Root, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return tool.Tool[Deps]{}, fmt.Errorf("pdfextract: root %s: %w", cfg.Root, err)
	}
	if !info.IsDir() {
		return tool.Tool[Deps]{}, fmt.Errorf("pdfextract: root %s is not a directory", cfg.Root)
	}
	if !cfg.Mode.Valid() {
		return tool.Tool[Deps]{}, fmt.Errorf("pdfextract: unknown OCR mode %q (want auto, prefer-text, or force-ocr)", cfg.Mode)
	}

	maxBytes := cfg.MaxBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxBytes
	}

	ocrEngine := cfg.OCREngine

	ext := &extractor{
		root:                   root,
		maxBytes:               maxBytes,
		imageDir:               cfg.ImageDir,
		returnImageParts:       cfg.ReturnImageParts,
		returnScannedPageParts: cfg.ReturnScannedPageParts,
		ocrEngine:              ocrEngine,
		mode:                   cfg.Mode,
		disableOCR:             cfg.DisableOCR,
	}

	return tool.New(tool.Tool[Deps]{
		Name:        ToolName,
		Description: ToolDescription,
		Schema:      schema,
		Exec: func(ctx context.Context, deps Deps, args json.RawMessage) (tool.Result, error) {
			return ext.execute(ctx, args)
		},
	})
}

// MustNew is New for tools declared as package-level values; it panics on invalid configuration.
func MustNew[Deps any](cfg Config) tool.Tool[Deps] {
	t, err := New[Deps](cfg)
	if err != nil {
		panic(err)
	}
	return t
}

func (e *extractor) execute(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}

	var input struct {
		Path  string `json:"path"`
		Pages string `json:"pages"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return tool.Result{}, &model.ModelRetry{Err: fmt.Errorf("arguments must be an object with a string path: %w", err)}
	}
	if input.Path == "" {
		return tool.Result{}, &model.ModelRetry{Err: fmt.Errorf("path is required")}
	}
	if filepath.IsAbs(input.Path) {
		return tool.Result{}, &model.ModelRetry{Err: fmt.Errorf("path must be relative to the root directory, got %q", input.Path)}
	}
	for _, seg := range strings.Split(input.Path, "/") {
		if seg == ".." {
			return tool.Result{}, &model.ModelRetry{Err: fmt.Errorf("path must stay within root directory, got %q", input.Path)}
		}
	}

	target := filepath.Join(e.root, filepath.FromSlash(input.Path))
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		if os.IsNotExist(err) {
			return tool.Result{}, &model.ModelRetry{Err: fmt.Errorf("no such file: %s", input.Path)}
		}
		return tool.Result{}, fmt.Errorf("pdfextract: %s: %w", input.Path, err)
	}
	if !withinDir(e.root, resolved) {
		return tool.Result{}, &model.ModelRetry{Err: fmt.Errorf("path must stay within root directory: %s resolves outside it", input.Path)}
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return tool.Result{}, fmt.Errorf("pdfextract: %s: %w", input.Path, err)
	}
	if info.IsDir() {
		return tool.Result{}, &model.ModelRetry{Err: fmt.Errorf("%s is a directory, not a PDF file", input.Path)}
	}

	doc, err := Extract(ctx, resolved, Options{
		Pages:                  input.Pages,
		ImageDir:               e.imageDir,
		ReturnImageParts:       e.returnImageParts,
		ReturnScannedPageParts: e.returnScannedPageParts,
		OCREngine:              e.ocrEngine,
		Mode:                   e.mode,
		DisableOCR:             e.disableOCR,
		MaxBytes:               e.maxBytes,
	})
	if err != nil {
		return tool.Result{}, err
	}

	text := doc.Markdown

	// Both options are independent: either may contribute images, and an image
	// selected by both is emitted once.
	var candidates []ImageRef
	if e.returnImageParts {
		candidates = append(candidates, doc.Images...)
	}
	if e.returnScannedPageParts {
		for _, p := range doc.Pages {
			if p.IsScanned && p.ScanImage != nil && len(p.ScanImage.Data) > 0 {
				candidates = append(candidates, *p.ScanImage)
			}
		}
	}
	seen := make(map[string]bool, len(candidates))
	var unique []ImageRef
	for _, img := range candidates {
		key := img.ID
		if key == "" {
			key = img.Path
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, img)
	}
	parts := BuildImageParts(unique)

	return tool.Result{
		Text:  text,
		Parts: parts,
	}, nil
}

// Extract parses a PDF file at path and returns the extracted Document.
func Extract(ctx context.Context, path string, opts Options) (*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pdfextract: read %s: %w", path, err)
	}
	return ExtractBytes(ctx, data, opts)
}

// ExtractBytes parses PDF bytes and returns the extracted Document.
func ExtractBytes(ctx context.Context, data []byte, opts Options) (*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	pdfDoc, err := ParsePDF(data)
	if err != nil {
		return nil, &UnsupportedFileError{Reason: err.Error(), Err: err}
	}

	numPages := pdfDoc.NumPages()
	pageIndices := parsePageRange(opts.Pages, numPages)

	doc := &Document{
		NumPages: numPages,
	}

	var docMarkdown strings.Builder

	for _, pageIdx := range pageIndices {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		parsedPage, err := pdfDoc.ExtractPage(pageIdx)
		if err != nil {
			// Keep the page slot and the error instead of dropping it: a caller
			// must be able to tell a 200-page document came back with 197 pages.
			marker := fmt.Sprintf("[Page %d: extraction failed]\n\n", pageIdx+1)
			docMarkdown.WriteString(marker)
			doc.Pages = append(doc.Pages, Page{
				Index:    pageIdx,
				Source:   PageSourceEmpty,
				Markdown: marker,
				Err:      err,
			})
			continue
		}

		// 1. Process Images & Match Captions
		pageImages, spansAfterImages := ProcessPageImages(parsedPage, opts.ImageDir, pageIdx)
		parsedPage.Spans = spansAfterImages

		// IsScanned reports what the page looks like, independent of what the
		// routing policy did with it. A scan we chose not to OCR is still a scan.
		detectedScan := IsScannedPage(parsedPage)
		isScan := detectedScan
		var scanImg *ImageRef
		for i := range pageImages {
			if pageImages[i].IsPageScan {
				if scanImg == nil || (pageImages[i].Width*pageImages[i].Height > scanImg.Width*scanImg.Height) {
					scanImg = &pageImages[i]
				}
			}
		}

		hasPageImage := (scanImg != nil && len(scanImg.Data) > 0) || len(parsedPage.Images) > 0
		digitalText := false
		for _, s := range parsedPage.Spans {
			if strings.TrimSpace(s.Text) != "" {
				digitalText = true
				break
			}
		}

		runOCR := false
		switch opts.Mode {
		case ModePreferText:
			runOCR = false
		case ModeForceOCR:
			runOCR = hasPageImage
		default: // "", ModeAuto
			runOCR = isScan
		}
		// DisableOCR wins over every mode; it is not part of detection.
		if opts.DisableOCR {
			runOCR = false
		}

		var pageUsage model.Usage
		var pageCost float64
		var directMarkdown string
		var ocrTables []Table
		ocrUsed := false

		// 2. Transcribe the page raster when the routing policy selects OCR.
		if runOCR {
			ocrEngine := opts.OCREngine

			var imgBytes []byte
			var format string
			if scanImg != nil && len(scanImg.Data) > 0 {
				imgBytes = scanImg.Data
				format = scanImg.Format
			} else if len(parsedPage.Images) > 0 {
				format, imgBytes = prepareImageData(parsedPage.Images[0])
			}

			if ocrEngine != nil && len(imgBytes) > 0 {
				if mdRec, ok := ocrEngine.(MarkdownRecognizer); ok {
					res, err := mdRec.RecognizeMarkdown(ctx, imgBytes, format, parsedPage.MediaBox)
					if err != nil {
						if !errors.Is(err, ErrNoOCREngine) {
							return nil, fmt.Errorf("pdfextract: ocr page %d: %w", pageIdx+1, err)
						}
					} else {
						directMarkdown = res.Markdown
						pageUsage = res.Usage
						pageCost = res.Cost
						for i := range res.Tables {
							res.Tables[i].Page = pageIdx
							ocrTables = append(ocrTables, res.Tables[i])
						}
						ocrUsed = directMarkdown != "" || len(ocrTables) > 0
					}
				} else {
					ocrSpans, err := ocrEngine.RecognizePage(ctx, imgBytes, format, parsedPage.MediaBox)
					if err != nil {
						if !errors.Is(err, ErrNoOCREngine) {
							return nil, fmt.Errorf("pdfextract: ocr page %d: %w", pageIdx+1, err)
						}
					} else if len(ocrSpans) > 0 {
						parsedPage.Spans = append(parsedPage.Spans, ocrSpans...)
						ocrUsed = true
					}
				}
			}
		}

		source := PageSourceEmpty
		switch {
		case ocrUsed && directMarkdown != "":
			source = PageSourceOCR
		case ocrUsed && digitalText:
			source = PageSourceHybrid
		case ocrUsed:
			source = PageSourceOCR
		case digitalText:
			source = PageSourceDigital
		}

		// 3. Convert thin horizontal rectangles into vector lines (fraction bars in LaTeX/Word PDFs)
		for _, r := range parsedPage.Rects {
			w := r.BBox.Width()
			h := r.BBox.Height()
			if h <= 2.5 && w >= 6.0 && w <= 140.0 {
				midY := (r.BBox.Y0 + r.BBox.Y1) / 2
				parsedPage.Lines = append(parsedPage.Lines, VectorLine{
					Start: Point{X: r.BBox.X0, Y: midY},
					End:   Point{X: r.BBox.X1, Y: midY},
				})
			}
		}

		// Reconstruct mathematical fractions from vector lines and text spans
		spansWithFractions, remainingLines := ReconstructFractions(parsedPage.Spans, parsedPage.Lines)
		parsedPage.Spans = spansWithFractions
		parsedPage.Lines = remainingLines

		// 4. Extract Intact Tables (Lattice, Booktabs & Stream)
		tables, nonTableSpans := ExtractTables(parsedPage)
		parsedPage.Spans = nonTableSpans

		// 5. Perform Layout Analysis & Reading Order Reconstruction
		blocks := AnalyzePageLayout(parsedPage, tables, pageImages)

		// Tables recovered by the OCR engine are not part of the vector layout,
		// so append them after layout analysis rather than feeding them into it.
		tables = append(tables, ocrTables...)

		// 6. Build Page Markdown
		var pageMD strings.Builder
		if numPages > 1 {
			pageMD.WriteString(fmt.Sprintf("## Page %d\n\n", pageIdx+1))
		}

		if directMarkdown != "" {
			pageMD.WriteString(directMarkdown)
			pageMD.WriteString("\n\n")
		} else if isScan && len(parsedPage.Spans) == 0 && len(blocks) == 0 {
			// Nothing to render: say so honestly instead of emitting a blank page.
			if opts.ReturnScannedPageParts {
				pageMD.WriteString("[Scanned page: image attached in tool parts for visual analysis]\n\n")
			} else {
				pageMD.WriteString("[Scanned page: OCR engine not configured]\n\n")
			}
		} else {
			for _, b := range blocks {
				switch b.Type {
				case BlockHeading:
					prefix := strings.Repeat("#", b.Level)
					pageMD.WriteString(fmt.Sprintf("%s %s\n\n", prefix, b.Text))
				case BlockList:
					pageMD.WriteString(fmt.Sprintf("%s\n", b.Text))
				case BlockTable:
					if pageMD.Len() > 0 && !strings.HasSuffix(pageMD.String(), "\n\n") {
						pageMD.WriteString("\n")
					}
					pageMD.WriteString(fmt.Sprintf("%s\n", b.Text))
				case BlockImage:
					pageMD.WriteString(fmt.Sprintf("%s\n\n", b.Text))
				case BlockParagraph:
					pageMD.WriteString(fmt.Sprintf("%s\n\n", b.Text))
				}
			}
		}

		pMarkdown := strings.TrimSpace(pageMD.String()) + "\n\n"
		docMarkdown.WriteString(pMarkdown)

		doc.Pages = append(doc.Pages, Page{
			Index:     pageIdx,
			Width:     parsedPage.MediaBox.Width(),
			Height:    parsedPage.MediaBox.Height(),
			IsScanned: isScan,
			Source:    source,
			ScanImage: scanImg,
			Markdown:  pMarkdown,
			Tables:    tables,
			Images:    pageImages,
			Blocks:    blocks,
			Usage:     pageUsage,
			Cost:      pageCost,
		})

		doc.Tables = append(doc.Tables, tables...)
		doc.Images = append(doc.Images, pageImages...)
		doc.Usage.InputTokens += pageUsage.InputTokens
		doc.Usage.OutputTokens += pageUsage.OutputTokens
		doc.Usage.CacheReadTokens += pageUsage.CacheReadTokens
		doc.Usage.CacheWriteTokens += pageUsage.CacheWriteTokens
		doc.Usage.ReasoningTokens += pageUsage.ReasoningTokens
		doc.Cost += pageCost
	}

	doc.Markdown = strings.TrimSpace(docMarkdown.String())
	if opts.MaxBytes > 0 && int64(len(doc.Markdown)) > opts.MaxBytes {
		doc.Markdown = truncateUTF8(doc.Markdown, opts.MaxBytes) +
			fmt.Sprintf("\n\n[pdfextract: output truncated at %d bytes]", opts.MaxBytes)
	}
	return doc, nil
}

// truncateUTF8 cuts s to at most max bytes without splitting a rune, so the
// result stays valid UTF-8 for the model that receives it.
func truncateUTF8(s string, max int64) string {
	if max <= 0 || int64(len(s)) <= max {
		return s
	}
	cut := int(max)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func parsePageRange(spec string, totalPages int) []int {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.EqualFold(spec, "all") {
		pages := make([]int, totalPages)
		for i := 0; i < totalPages; i++ {
			pages[i] = i
		}
		return pages
	}

	var pages []int
	parts := strings.Split(spec, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) == 2 {
				start, err1 := strconv.Atoi(strings.TrimSpace(rangeParts[0]))
				end, err2 := strconv.Atoi(strings.TrimSpace(rangeParts[1]))
				if err1 == nil && err2 == nil {
					for p := start; p <= end; p++ {
						if p >= 1 && p <= totalPages {
							pages = append(pages, p-1)
						}
					}
				}
			}
		} else {
			if p, err := strconv.Atoi(part); err == nil {
				if p >= 1 && p <= totalPages {
					pages = append(pages, p-1)
				}
			}
		}
	}

	if len(pages) == 0 {
		pages = make([]int, totalPages)
		for i := 0; i < totalPages; i++ {
			pages[i] = i
		}
	}

	return pages
}

func withinDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
