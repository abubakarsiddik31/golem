// Package docextract provides a high-performance, multi-format document
// extraction tool and library for Golem. It extracts clean, structured
// Markdown from Word (.docx), Excel (.xlsx), PowerPoint (.pptx), PDF (.pdf),
// Markdown (.md), CSV (.csv), TSV (.tsv), and plain text documents.
//
// The tool is an ordinary tool.Tool[Deps]: it composes with any agent
// dependency type and every agent option. Its name, description, and
// argument schema are public contract — models and prompts depend on
// them staying stable.
package docextract

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/pdfextract"
	"github.com/abubakarsiddik31/golem/tool"
)

// ToolName is the name of the tool New returns.
const ToolName = "extract_doc"

// ToolDescription is the description the model sees for the tool.
const ToolDescription = "Extract structured, readable Markdown from documents including Word (.docx), Excel (.xlsx), PowerPoint (.pptx), PDF (.pdf), Markdown (.md), CSV (.csv), and text files. Supports full text, outline/TOC extraction, and section/sheet/slide filtering."

// DefaultMaxBytes bounds extraction text output when Config.MaxBytes is zero.
const DefaultMaxBytes int64 = 2 << 20 // 2 MiB

// Format identifies supported document formats.
type Format string

const (
	FormatDocx     Format = "docx"
	FormatXlsx     Format = "xlsx"
	FormatPptx     Format = "pptx"
	FormatPDF      Format = "pdf"
	FormatMarkdown Format = "markdown"
	FormatCSV      Format = "csv"
	FormatTSV      Format = "tsv"
	FormatText     Format = "text"
	FormatUnknown  Format = "unknown"
)

// schema describes the tool's arguments; it is public contract.
var schema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "Path to the document relative to the root directory (.docx, .xlsx, .pptx, .pdf, .md, .txt, .csv, .tsv)."
		},
		"outline": {
			"type": "boolean",
			"description": "If true, extracts only the document outline / table of contents / slide titles / sheet names instead of the full text."
		},
		"query": {
			"type": "string",
			"description": "Optional search term to filter specific sections, headings, slides, sheets, or table rows."
		},
		"pages": {
			"type": "string",
			"description": "Optional page, slide, or sheet filter (e.g. '1-3', '5', 'Sheet1')."
		}
	},
	"required": ["path"],
	"additionalProperties": false
}`)

// Config configures the document extraction tool. Root is required.
type Config struct {
	// Root is the directory every extraction is confined to. It must exist
	// and be a directory; symlinks in it are resolved at construction.
	Root string
	// MaxBytes caps text output returned to the model; a longer output is
	// truncated, not failed, and the result ends with a truncation marker.
	// Zero selects DefaultMaxBytes; negative values fail New.
	MaxBytes int64
	// MaxRowsPerSheet bounds the number of tabular rows extracted per sheet or table.
	// Defaults to 200 when zero.
	MaxRowsPerSheet int
}

// Options configures standalone document extraction.
type Options struct {
	// Outline returns only the structural outline (headings, slides, sheets) when true.
	Outline bool
	// Query filters the output to sections matching the search query.
	Query string
	// Pages filters extraction to specific page numbers, slide ranges, or sheet names.
	Pages string
	// MaxBytes caps output text length.
	MaxBytes int64
	// MaxRowsPerSheet caps tabular row count.
	MaxRowsPerSheet int
}

// Document represents an extracted document.
type Document struct {
	// Format is the detected or requested document format.
	Format Format `json:"format"`
	// Title is the primary document title or first heading if detected.
	Title string `json:"title,omitempty"`
	// Content is the extracted Markdown text.
	Content string `json:"content"`
	// Outline lists structural headings, slides, or sheets.
	Outline []OutlineItem `json:"outline,omitempty"`
	// Metadata contains document-specific key-value pairs.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// OutlineItem represents a structural heading, slide, or sheet.
type OutlineItem struct {
	Level    int    `json:"level"`
	Title    string `json:"title"`
	Location string `json:"location,omitempty"`
}

// RenderOutline renders the outline items as a hierarchical Markdown list.
func (d *Document) RenderOutline() string {
	if len(d.Outline) == 0 {
		return "# Document Outline\n\n*No headings or structural outline found.*"
	}
	var sb strings.Builder
	sb.WriteString("# Document Outline\n\n")
	for _, item := range d.Outline {
		indent := ""
		if item.Level > 1 {
			indent = strings.Repeat("  ", item.Level-1)
		}
		loc := ""
		if item.Location != "" {
			loc = fmt.Sprintf(" *(%s)*", item.Location)
		}
		sb.WriteString(fmt.Sprintf("%s- %s%s\n", indent, item.Title, loc))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// extractor holds the resolved configuration behind the tool's exec.
type extractor struct {
	root            string
	maxBytes        int64
	maxRowsPerSheet int
}

// New validates cfg and returns the extract_doc tool ready for
// registration with an agent. Deps is the agent's dependency type; the
// extraction itself does not use it, but the tool carries it so one
// constructor serves every agent.
func New[Deps any](cfg Config) (tool.Tool[Deps], error) {
	if cfg.MaxBytes < 0 {
		return tool.Tool[Deps]{}, fmt.Errorf("docextract: max bytes must not be negative, got %d", cfg.MaxBytes)
	}
	if cfg.Root == "" {
		return tool.Tool[Deps]{}, fmt.Errorf("docextract: root is required")
	}
	root, err := filepath.EvalSymlinks(cfg.Root)
	if err != nil {
		return tool.Tool[Deps]{}, fmt.Errorf("docextract: root %s: %w", cfg.Root, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return tool.Tool[Deps]{}, fmt.Errorf("docextract: root %s: %w", cfg.Root, err)
	}
	if !info.IsDir() {
		return tool.Tool[Deps]{}, fmt.Errorf("docextract: root %s is not a directory", cfg.Root)
	}
	maxBytes := cfg.MaxBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxBytes
	}
	maxRows := cfg.MaxRowsPerSheet
	if maxRows <= 0 {
		maxRows = 200
	}

	e := &extractor{
		root:            root,
		maxBytes:        maxBytes,
		maxRowsPerSheet: maxRows,
	}

	return tool.New(tool.Tool[Deps]{
		Name:        ToolName,
		Description: ToolDescription,
		Schema:      schema,
		Exec: func(ctx context.Context, deps Deps, args json.RawMessage) (tool.Result, error) {
			text, err := e.extract(ctx, args)
			return tool.Text(text), err
		},
	})
}

// MustNew is New for tools declared as package-level values; it panics
// on invalid configuration.
func MustNew[Deps any](cfg Config) tool.Tool[Deps] {
	validated, err := New[Deps](cfg)
	if err != nil {
		panic(err)
	}
	return validated
}

func (e *extractor) extract(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	input, err := parseArgs(args)
	if err != nil {
		return "", err
	}

	target, err := e.resolve(input.Path)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &model.ModelRetry{Err: fmt.Errorf("no such file: %s", input.Path)}
		}
		return "", fmt.Errorf("docextract: %s: %w", input.Path, err)
	}
	if info.IsDir() {
		return "", &model.ModelRetry{Err: fmt.Errorf("%s is a directory, not a file", input.Path)}
	}
	if !info.Mode().IsRegular() {
		return "", &model.ModelRetry{Err: fmt.Errorf("%s is not a regular file", input.Path)}
	}

	opts := Options{
		Outline:         input.Outline,
		Query:           input.Query,
		Pages:           input.Pages,
		MaxBytes:        e.maxBytes,
		MaxRowsPerSheet: e.maxRowsPerSheet,
	}

	doc, err := Extract(ctx, target, opts)
	if err != nil {
		return "", err
	}

	var output string
	if opts.Outline {
		output = doc.RenderOutline()
	} else {
		output = doc.Content
	}

	if int64(len(output)) > e.maxBytes {
		output = output[:e.maxBytes] + fmt.Sprintf("\n\n[docextract: document truncated at %d bytes]", e.maxBytes)
	}

	return output, nil
}

type toolInput struct {
	Path    string `json:"path"`
	Outline bool   `json:"outline"`
	Query   string `json:"query"`
	Pages   string `json:"pages"`
}

func parseArgs(args json.RawMessage) (toolInput, error) {
	var input toolInput
	if err := json.Unmarshal(args, &input); err != nil {
		return toolInput{}, &model.ModelRetry{Err: fmt.Errorf("arguments must be an object: %w", err)}
	}
	if input.Path == "" {
		return toolInput{}, &model.ModelRetry{Err: fmt.Errorf("path is required")}
	}
	if filepath.IsAbs(input.Path) {
		return toolInput{}, &model.ModelRetry{Err: fmt.Errorf("path must be relative to the root directory, got %q", input.Path)}
	}
	for _, segment := range strings.Split(input.Path, "/") {
		if segment == ".." {
			return toolInput{}, &model.ModelRetry{Err: fmt.Errorf("path must stay within the root directory, got %q", input.Path)}
		}
	}
	return input, nil
}

func (e *extractor) resolve(path string) (string, error) {
	target := filepath.Join(e.root, filepath.FromSlash(path))
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &model.ModelRetry{Err: fmt.Errorf("no such file: %s", path)}
		}
		return "", fmt.Errorf("docextract: %s: %w", path, err)
	}
	if !withinDir(e.root, resolved) {
		return "", &model.ModelRetry{Err: fmt.Errorf("path must stay within the root directory: %s resolves outside it", path)}
	}
	return resolved, nil
}

func withinDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Extract extracts a document from the local filesystem at targetPath.
func Extract(ctx context.Context, targetPath string, opts Options) (*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(targetPath)
	if err != nil {
		return nil, fmt.Errorf("docextract: read %s: %w", targetPath, err)
	}
	return ExtractBytes(ctx, data, filepath.Base(targetPath), opts)
}

// ExtractBytes extracts a document from an in-memory byte slice with a hint filename.
func ExtractBytes(ctx context.Context, data []byte, filename string, opts Options) (*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	format := DetectFormat(data, filename)

	switch format {
	case FormatDocx:
		r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, fmt.Errorf("docextract: parse docx zip: %w", err)
		}
		return extractDocx(r, opts)

	case FormatXlsx:
		r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, fmt.Errorf("docextract: parse xlsx zip: %w", err)
		}
		return extractXlsx(r, opts)

	case FormatPptx:
		r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, fmt.Errorf("docextract: parse pptx zip: %w", err)
		}
		return extractPptx(r, opts)

	case FormatPDF:
		pdfDoc, err := pdfextract.ExtractBytes(ctx, data, pdfextract.Options{
			Pages:    opts.Pages,
			MaxBytes: opts.MaxBytes,
		})
		if err != nil {
			return nil, fmt.Errorf("docextract: pdf extraction: %w", err)
		}
		content := pdfDoc.Markdown
		if opts.Query != "" {
			filtered := extractSectionByQuery(content, opts.Query)
			if filtered != "" {
				content = filtered
			}
		}
		var outline []OutlineItem
		for _, p := range pdfDoc.Pages {
			outline = append(outline, OutlineItem{
				Level:    1,
				Title:    fmt.Sprintf("Page %d", p.Index+1),
				Location: fmt.Sprintf("%d blocks", len(p.Blocks)),
			})
		}
		title := ""
		if len(outline) > 0 {
			title = outline[0].Title
		}
		return &Document{
			Format:   FormatPDF,
			Title:    title,
			Content:  content,
			Outline:  outline,
			Metadata: map[string]string{"num_pages": fmt.Sprintf("%d", pdfDoc.NumPages)},
		}, nil

	case FormatMarkdown:
		return extractMarkdown(bytes.NewReader(data), opts)

	case FormatCSV:
		return extractCSV(bytes.NewReader(data), ',', FormatCSV, opts)

	case FormatTSV:
		return extractCSV(bytes.NewReader(data), '\t', FormatTSV, opts)

	case FormatText:
		return extractPlainText(bytes.NewReader(data), opts)

	default:
		// Try as markdown/text
		return extractMarkdown(bytes.NewReader(data), opts)
	}
}

// DetectFormat detects document format using filename extension and magic bytes.
func DetectFormat(data []byte, filename string) Format {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".docx":
		return FormatDocx
	case ".xlsx":
		return FormatXlsx
	case ".pptx":
		return FormatPptx
	case ".pdf":
		return FormatPDF
	case ".md", ".markdown":
		return FormatMarkdown
	case ".csv":
		return FormatCSV
	case ".tsv":
		return FormatTSV
	case ".txt", ".log", ".json", ".yaml", ".yml", ".xml", ".html":
		return FormatText
	}

	// Sniff magic bytes
	if bytes.HasPrefix(data, []byte("%PDF-")) {
		return FormatPDF
	}
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		// Inspect zip file entries to distinguish docx / xlsx / pptx
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err == nil {
			for _, f := range zr.File {
				if strings.HasPrefix(f.Name, "word/") {
					return FormatDocx
				}
				if strings.HasPrefix(f.Name, "xl/") {
					return FormatXlsx
				}
				if strings.HasPrefix(f.Name, "ppt/") {
					return FormatPptx
				}
			}
		}
	}

	return FormatUnknown
}
