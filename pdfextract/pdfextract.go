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
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
	// Nil defaults to DefaultOCREngine().
	OCREngine OCREngine

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
	DisableOCR             bool
	MaxBytes               int64
}

// Page holds extracted elements and Markdown text for one page.
type Page struct {
	Index     int
	Width     float64
	Height    float64
	IsScanned bool      // true if the page was detected as a scanned raster document
	ScanImage *ImageRef // primary scanned page image, when IsScanned is true
	Markdown  string
	Tables    []Table
	Images    []ImageRef
	Blocks    []LayoutBlock
	Usage     model.Usage // token usage incurred for this page (e.g. from ModelOCREngine)
	Cost      float64     // monetary cost incurred for this page in US dollars
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
}

func (e *UnsupportedFileError) Error() string {
	return fmt.Sprintf("pdfextract: %s: invalid PDF file: %s", e.Path, e.Reason)
}

// extractor holds resolved configuration for tool execution.
type extractor struct {
	root                   string
	maxBytes               int64
	imageDir               string
	returnImageParts       bool
	returnScannedPageParts bool
	ocrEngine              OCREngine
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

	maxBytes := cfg.MaxBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxBytes
	}

	ocrEngine := cfg.OCREngine
	if ocrEngine == nil && !cfg.DisableOCR && !cfg.ReturnScannedPageParts {
		ocrEngine = DefaultOCREngine()
	}

	ext := &extractor{
		root:                   root,
		maxBytes:               maxBytes,
		imageDir:               cfg.ImageDir,
		returnImageParts:       cfg.ReturnImageParts,
		returnScannedPageParts: cfg.ReturnScannedPageParts,
		ocrEngine:              ocrEngine,
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
		DisableOCR:             e.disableOCR,
		MaxBytes:               e.maxBytes,
	})
	if err != nil {
		return tool.Result{}, err
	}

	text := doc.Markdown
	if int64(len(text)) > e.maxBytes {
		text = text[:e.maxBytes] + fmt.Sprintf("\n\n[pdfextract: output truncated at %d bytes]", e.maxBytes)
	}

	var parts []model.Part
	if e.returnImageParts {
		var figImages []ImageRef
		for _, img := range doc.Images {
			if !img.IsPageScan {
				figImages = append(figImages, img)
			}
		}
		if len(figImages) > 0 {
			parts = append(parts, BuildImageParts(figImages)...)
		}
	}
	if e.returnScannedPageParts {
		var scanImages []ImageRef
		for _, p := range doc.Pages {
			if p.IsScanned && p.ScanImage != nil && len(p.ScanImage.Data) > 0 {
				scanImages = append(scanImages, *p.ScanImage)
			}
		}
		if len(scanImages) > 0 {
			parts = append(parts, BuildImageParts(scanImages)...)
		}
	}

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
		return nil, &UnsupportedFileError{Reason: err.Error()}
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
			continue
		}

		// 1. Process Images & Match Captions
		pageImages, spansAfterImages := ProcessPageImages(parsedPage, opts.ImageDir, pageIdx)
		parsedPage.Spans = spansAfterImages

		isScan := !opts.DisableOCR && IsScannedPage(parsedPage)
		var scanImg *ImageRef
		for i := range pageImages {
			if pageImages[i].IsPageScan {
				scanImg = &pageImages[i]
				break
			}
		}

		var pageUsage model.Usage
		var pageCost float64
		var directMarkdown string

		// 2. Check if page is a pure scan
		if isScan {
			ocrEngine := opts.OCREngine
			if ocrEngine == nil && !opts.ReturnScannedPageParts {
				ocrEngine = DefaultOCREngine()
			}

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
					if err == nil && len(res.Markdown) > 0 {
						directMarkdown = res.Markdown
						pageUsage = res.Usage
						pageCost = res.Cost
					}
				} else {
					ocrSpans, err := ocrEngine.RecognizePage(ctx, imgBytes, format, parsedPage.MediaBox)
					if err == nil && len(ocrSpans) > 0 {
						parsedPage.Spans = append(parsedPage.Spans, ocrSpans...)
					}
				}
			}
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

		// 6. Build Page Markdown
		var pageMD strings.Builder
		if numPages > 1 {
			pageMD.WriteString(fmt.Sprintf("## Page %d\n\n", pageIdx+1))
		}

		if directMarkdown != "" {
			pageMD.WriteString(directMarkdown)
			pageMD.WriteString("\n\n")
		} else if isScan && len(parsedPage.Spans) == 0 {
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
	return doc, nil
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
