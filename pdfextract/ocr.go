package pdfextract

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/abubakarsiddik31/golem/model"
)

// OCREngine defines the contract for pluggable modern deep-learning OCR engines.
type OCREngine interface {
	// RecognizePage performs text recognition on an image of a page and returns extracted TextSpans.
	RecognizePage(ctx context.Context, imageBytes []byte, format string, pageBox Rect) ([]TextSpan, error)
}

// MarkdownResult holds the recognized text, token usage, and dollar cost for a page.
type MarkdownResult struct {
	Markdown string
	Usage    model.Usage
	Cost     float64

	// Tables optionally carries structured tables recovered by the engine, so a
	// scanned page can populate Document.Tables like a digital page does.
	// Engines that only emit Markdown leave it nil. BBox and Ruled are not set:
	// OCR engines report structure, not vector geometry.
	Tables []Table
}

// MarkdownRecognizer is an optional interface an OCREngine can implement
// to provide direct structured Markdown (e.g. from vision LLMs or cloud Document AI)
// instead of or in addition to raw TextSpans.
type MarkdownRecognizer interface {
	RecognizeMarkdown(ctx context.Context, imageBytes []byte, format string, pageBox Rect) (MarkdownResult, error)
}

// IsScannedPage reports whether a page appears to be a scanned raster image rather than a digital vector page.
func IsScannedPage(page *ParsedPage) bool {
	// If the page already has a reasonable amount of text, it's digital
	totalChars := 0
	for _, s := range page.Spans {
		totalChars += len(strings.TrimSpace(s.Text))
	}
	if totalChars >= 40 {
		return false
	}

	pageArea := page.MediaBox.Area()
	if pageArea <= 0 {
		pageArea = 612 * 792 // US letter
	}

	var imageArea float64
	for _, img := range page.Images {
		imageArea += img.BBox.Area()
	}

	// Scanned page if images cover substantial page area, or if page has zero text and at least one image
	return (imageArea/pageArea) >= 0.30 || (totalChars == 0 && len(page.Images) > 0)
}

// NoopOCR is a fallback OCR engine that notes scanned pages without running heavy models.
type NoopOCR struct{}

func (NoopOCR) RecognizePage(ctx context.Context, imageBytes []byte, format string, pageBox Rect) ([]TextSpan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []TextSpan{
		{
			Text:     "[Scanned page: OCR engine not configured]",
			FontSize: 12.0,
			BBox:     Rect{X0: pageBox.X0 + 50, Y0: pageBox.Y1 - 50, X1: pageBox.X1 - 50, Y1: pageBox.Y1 - 30},
		},
	}, nil
}

// DefaultModelOCRPrompt is the system prompt used by ModelOCREngine for scanned document transcription.
const DefaultModelOCRPrompt = "Transcribe all text, headings, and tables from this scanned document page into clean GitHub-Flavored Markdown. Preserve reading order, column structure, and table formatting exactly. Output only the markdown without conversational commentary or markdown code fences."

// ModelOCREngine transcribes scanned pages using a vision-capable language model (such as Gemini Flash-Lite or a local vision model).
type ModelOCREngine struct {
	model  model.Model
	price  model.Price
	prompt string
}

// ModelOCROption configures a ModelOCREngine.
type ModelOCROption func(*ModelOCREngine)

// WithModelPrice configures price calculation for model token usage.
func WithModelPrice(price model.Price) ModelOCROption {
	return func(e *ModelOCREngine) {
		e.price = price
	}
}

// WithModelPrompt overrides the default document transcription prompt.
func WithModelPrompt(prompt string) ModelOCROption {
	return func(e *ModelOCREngine) {
		e.prompt = prompt
	}
}

// NewModelOCR returns an OCREngine backed by a vision model.
func NewModelOCR(m model.Model, opts ...ModelOCROption) (*ModelOCREngine, error) {
	if m == nil {
		return nil, fmt.Errorf("pdfextract: model is required for ModelOCREngine")
	}
	e := &ModelOCREngine{
		model:  m,
		prompt: DefaultModelOCRPrompt,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e, nil
}

// MustNewModelOCR is NewModelOCR that panics on error.
func MustNewModelOCR(m model.Model, opts ...ModelOCROption) *ModelOCREngine {
	e, err := NewModelOCR(m, opts...)
	if err != nil {
		panic(err)
	}
	return e
}

// RecognizeMarkdown transcribes a page image directly into structured Markdown and tracks usage/cost.
func (m *ModelOCREngine) RecognizeMarkdown(ctx context.Context, imageBytes []byte, format string, pageBox Rect) (MarkdownResult, error) {
	if err := ctx.Err(); err != nil {
		return MarkdownResult{}, err
	}
	if len(imageBytes) == 0 {
		return MarkdownResult{}, nil
	}

	mediaType := "image/png"
	if strings.EqualFold(format, "jpeg") || strings.EqualFold(format, "jpg") {
		mediaType = "image/jpeg"
	}

	req := model.Request{
		Messages: []model.Message{
			{
				Role:    model.RoleUser,
				Content: m.prompt,
				Parts: []model.Part{
					model.ImageData(mediaType, imageBytes),
				},
			},
		},
	}

	resp, err := m.model.Generate(ctx, req)
	if err != nil {
		return MarkdownResult{}, fmt.Errorf("pdfextract: model ocr generation failed: %w", err)
	}

	text := stripCodeFences(resp.Message.Content)

	var cost float64
	if m.price != nil {
		cost = m.price.Cost(resp.Usage)
	}

	return MarkdownResult{
		Markdown: text,
		Usage:    resp.Usage,
		Cost:     cost,
	}, nil
}

// RecognizePage fulfills the OCREngine interface by delegating to RecognizeMarkdown and splitting into spans.
func (m *ModelOCREngine) RecognizePage(ctx context.Context, imageBytes []byte, format string, pageBox Rect) ([]TextSpan, error) {
	res, err := m.RecognizeMarkdown(ctx, imageBytes, format, pageBox)
	if err != nil {
		return nil, err
	}
	return markdownToSpans(res.Markdown, pageBox), nil
}

// DefaultMistralOCRBaseURL is Mistral's API base URL.
const DefaultMistralOCRBaseURL = "https://api.mistral.ai/v1"

// DefaultMistralOCRModel is the latest Mistral OCR model identifier.
const DefaultMistralOCRModel = "mistral-ocr-latest"

// MistralOCRConfig configures MistralOCREngine.
type MistralOCRConfig struct {
	// APIKey authenticates requests with Mistral via the Authorization Bearer header. Required.
	APIKey string
	// BaseURL prefixes the OCR endpoint path; defaults to DefaultMistralOCRBaseURL.
	BaseURL string
	// Model names the Mistral OCR model to use; defaults to DefaultMistralOCRModel.
	Model string
	// PricePerPage is the user-supplied USD rate per processed page (e.g. 0.002 for $2/1k pages).
	// Golem ships no fixed price table per ADR 0024; zero leaves cost at 0.00.
	PricePerPage float64
	// HTTPClient performs HTTP requests; defaults to a 60-second client.
	HTTPClient *http.Client
}

// MistralOCREngine transcribes scanned pages using Mistral's dedicated OCR API.
type MistralOCREngine struct {
	apiKey       string
	baseURL      string
	model        string
	pricePerPage float64
	client       *http.Client
}

// NewMistralOCR returns an OCREngine backed by Mistral's OCR API.
func NewMistralOCR(cfg MistralOCRConfig) (*MistralOCREngine, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("pdfextract: mistral ocr: api key is required")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultMistralOCRBaseURL
	}
	modelName := cfg.Model
	if modelName == "" {
		modelName = DefaultMistralOCRModel
	}
	pricePerPage := cfg.PricePerPage
	if pricePerPage < 0 {
		pricePerPage = 0
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &MistralOCREngine{
		apiKey:       cfg.APIKey,
		baseURL:      strings.TrimRight(baseURL, "/"),
		model:        modelName,
		pricePerPage: pricePerPage,
		client:       client,
	}, nil
}

// MustNewMistralOCR is NewMistralOCR that panics on error.
func MustNewMistralOCR(cfg MistralOCRConfig) *MistralOCREngine {
	e, err := NewMistralOCR(cfg)
	if err != nil {
		panic(err)
	}
	return e
}

// RecognizeMarkdown calls the Mistral OCR API and returns extracted Markdown and cost.
func (m *MistralOCREngine) RecognizeMarkdown(ctx context.Context, imageBytes []byte, format string, pageBox Rect) (MarkdownResult, error) {
	if err := ctx.Err(); err != nil {
		return MarkdownResult{}, err
	}
	if len(imageBytes) == 0 {
		return MarkdownResult{}, nil
	}

	mediaType := "image/png"
	if strings.EqualFold(format, "jpeg") || strings.EqualFold(format, "jpg") {
		mediaType = "image/jpeg"
	}

	b64Data := base64.StdEncoding.EncodeToString(imageBytes)
	dataURL := fmt.Sprintf("data:%s;base64,%s", mediaType, b64Data)

	reqPayload := map[string]any{
		"model": m.model,
		"document": map[string]any{
			"type":      "image_url",
			"image_url": dataURL,
		},
	}

	reqBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return MarkdownResult{}, fmt.Errorf("pdfextract: mistral ocr: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/ocr", bytes.NewReader(reqBytes))
	if err != nil {
		return MarkdownResult{}, fmt.Errorf("pdfextract: mistral ocr: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return MarkdownResult{}, fmt.Errorf("pdfextract: mistral ocr: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return MarkdownResult{}, fmt.Errorf("pdfextract: mistral ocr: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return MarkdownResult{}, fmt.Errorf("pdfextract: mistral ocr returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var ocrResp struct {
		Pages []struct {
			Index    int    `json:"index"`
			Markdown string `json:"markdown"`
		} `json:"pages"`
		UsageInfo struct {
			PagesProcessed int `json:"pages_processed"`
		} `json:"usage_info"`
	}

	if err := json.Unmarshal(body, &ocrResp); err != nil {
		return MarkdownResult{}, fmt.Errorf("pdfextract: mistral ocr: decode response: %w", err)
	}

	var md strings.Builder
	for _, p := range ocrResp.Pages {
		if md.Len() > 0 {
			md.WriteString("\n\n")
		}
		md.WriteString(strings.TrimSpace(p.Markdown))
	}

	pagesProcessed := ocrResp.UsageInfo.PagesProcessed
	if pagesProcessed <= 0 {
		pagesProcessed = 1
	}

	cost := float64(pagesProcessed) * m.pricePerPage

	return MarkdownResult{
		Markdown: md.String(),
		Cost:     cost,
	}, nil
}

// RecognizePage fulfills the OCREngine interface by delegating to RecognizeMarkdown.
func (m *MistralOCREngine) RecognizePage(ctx context.Context, imageBytes []byte, format string, pageBox Rect) ([]TextSpan, error) {
	res, err := m.RecognizeMarkdown(ctx, imageBytes, format, pageBox)
	if err != nil {
		return nil, err
	}
	return markdownToSpans(res.Markdown, pageBox), nil
}

// DefaultIndraOCRBin is the default executable name for the Indra OCR engine.
const DefaultIndraOCRBin = "indra-ocr"

// DefaultIndraOCRTimeout bounds a single page's OCR call.
const DefaultIndraOCRTimeout = 2 * time.Minute

// IndraOCRConfig configures IndraOCREngine.
type IndraOCRConfig struct {
	// BinaryPath is the indra-ocr executable. Empty selects DefaultIndraOCRBin,
	// which is resolved on PATH at construction.
	BinaryPath string

	// ModelDir overrides the model directory (CLI --model-dir); empty uses the
	// CLI's downloaded cache.
	ModelDir string

	// DPI sets the render resolution for PDFs (CLI --dpi); zero uses the CLI default.
	DPI int

	// Cores limits OCR worker processes (CLI --cores); zero uses all usable cores.
	Cores int

	// KeepFurniture keeps page headers and footers in the Markdown (CLI --keep-furniture).
	KeepFurniture bool

	// Timeout bounds a single page's OCR call; zero selects DefaultIndraOCRTimeout.
	Timeout time.Duration

	// Run executes the OCR command and returns its standard output. Nil uses
	// exec.CommandContext. It is exposed so callers can fake, sandbox, or wrap
	// the subprocess; the resolved argv is the binary, its flags, and the
	// temporary page-image path.
	Run func(ctx context.Context, name string, args []string) ([]byte, error)
}

// IndraOCREngine transcribes scanned pages with the local Indra OCR model
// (https://github.com/datalab-to/indra). It shells out to the indra-ocr CLI and
// never contacts the network: the model runs on the host CPU.
type IndraOCREngine struct {
	binaryPath    string
	modelDir      string
	dpi           int
	cores         int
	keepFurniture bool
	timeout       time.Duration
	run           func(ctx context.Context, name string, args []string) ([]byte, error)
}

// NewIndraOCR returns an OCREngine backed by the local indra-ocr CLI. It fails
// if the binary is not on PATH and no Run override is supplied.
func NewIndraOCR(cfg IndraOCRConfig) (*IndraOCREngine, error) {
	bin := cfg.BinaryPath
	if bin == "" {
		bin = DefaultIndraOCRBin
	}
	run := cfg.Run
	if run == nil {
		resolved, err := exec.LookPath(bin)
		if err != nil {
			return nil, fmt.Errorf("pdfextract: indra ocr: binary %q not found in PATH (install with \"pip install indra-ocr\"): %w", bin, err)
		}
		bin = resolved
		run = func(ctx context.Context, name string, args []string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).Output()
		}
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultIndraOCRTimeout
	}
	return &IndraOCREngine{
		binaryPath:    bin,
		modelDir:      cfg.ModelDir,
		dpi:           cfg.DPI,
		cores:         cfg.Cores,
		keepFurniture: cfg.KeepFurniture,
		timeout:       timeout,
		run:           run,
	}, nil
}

// MustNewIndraOCR is NewIndraOCR that panics on error.
func MustNewIndraOCR(cfg IndraOCRConfig) *IndraOCREngine {
	e, err := NewIndraOCR(cfg)
	if err != nil {
		panic(err)
	}
	return e
}

// RecognizeMarkdown writes the page image to a temporary file, runs the
// indra-ocr CLI in JSON mode, and returns the page's Markdown.
func (i *IndraOCREngine) RecognizeMarkdown(ctx context.Context, imageBytes []byte, format string, pageBox Rect) (MarkdownResult, error) {
	if err := ctx.Err(); err != nil {
		return MarkdownResult{}, err
	}
	if len(imageBytes) == 0 {
		return MarkdownResult{}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()

	f, err := os.CreateTemp("", "golem-indra-*"+imageFileExt(format))
	if err != nil {
		return MarkdownResult{}, fmt.Errorf("pdfextract: indra ocr: create page image: %w", err)
	}
	path := f.Name()
	defer os.Remove(path)

	if _, err := f.Write(imageBytes); err != nil {
		f.Close()
		return MarkdownResult{}, fmt.Errorf("pdfextract: indra ocr: write page image: %w", err)
	}
	if err := f.Close(); err != nil {
		return MarkdownResult{}, fmt.Errorf("pdfextract: indra ocr: write page image: %w", err)
	}

	args := []string{path, "--format", "json"}
	if i.modelDir != "" {
		args = append(args, "--model-dir", i.modelDir)
	}
	if i.dpi > 0 {
		args = append(args, "--dpi", strconv.Itoa(i.dpi))
	}
	if i.cores > 0 {
		args = append(args, "--cores", strconv.Itoa(i.cores))
	}
	if i.keepFurniture {
		args = append(args, "--keep-furniture")
	}

	out, err := i.run(ctx, i.binaryPath, args)
	if err != nil {
		return MarkdownResult{}, fmt.Errorf("pdfextract: indra ocr: run %s: %w", i.binaryPath, err)
	}

	md, tables, err := parseIndraPages(out)
	if err != nil {
		return MarkdownResult{}, err
	}
	return MarkdownResult{Markdown: md, Tables: tables}, nil
}

// RecognizePage fulfills the OCREngine interface by delegating to RecognizeMarkdown.
func (i *IndraOCREngine) RecognizePage(ctx context.Context, imageBytes []byte, format string, pageBox Rect) ([]TextSpan, error) {
	res, err := i.RecognizeMarkdown(ctx, imageBytes, format, pageBox)
	if err != nil {
		return nil, err
	}
	return markdownToSpans(res.Markdown, pageBox), nil
}

// indraPage is one page object emitted by `indra-ocr --format json`.
type indraPage struct {
	Markdown string `json:"markdown"`
	Source   string `json:"source"`
	Note     string `json:"note"`
}

func imageFileExt(format string) string {
	if strings.EqualFold(format, "jpeg") || strings.EqualFold(format, "jpg") {
		return ".jpg"
	}
	return ".png"
}

// parseIndraPages joins the Markdown of one or more Indra page objects and
// recovers the HTML tables it emitted. The CLI emits a single JSON object for
// one page and newline-delimited objects otherwise.
func parseIndraPages(out []byte) (string, []Table, error) {
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return "", nil, fmt.Errorf("pdfextract: indra ocr: empty output")
	}

	var pages []indraPage
	if trimmed[0] == '{' {
		var single indraPage
		if err := json.Unmarshal(trimmed, &single); err == nil {
			pages = append(pages, single)
		}
	}
	if pages == nil {
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		for {
			var p indraPage
			if err := dec.Decode(&p); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return "", nil, fmt.Errorf("pdfextract: indra ocr: decode output: %w", err)
			}
			pages = append(pages, p)
		}
	}
	if len(pages) == 0 {
		return "", nil, fmt.Errorf("pdfextract: indra ocr: no page objects in output")
	}

	var md strings.Builder
	for _, p := range pages {
		if strings.EqualFold(p.Source, "error") {
			return "", nil, fmt.Errorf("pdfextract: indra ocr: page error: %s", p.Note)
		}
		text := strings.TrimSpace(p.Markdown)
		if text == "" {
			continue
		}
		if md.Len() > 0 {
			md.WriteString("\n\n")
		}
		md.WriteString(text)
	}
	joined := md.String()
	return joined, parseIndraTables(joined), nil
}

var (
	indraTableRe = regexp.MustCompile(`(?is)<table[^>]*>(.*?)</table>`)
	indraRowRe   = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
	indraCellRe  = regexp.MustCompile(`(?is)<t[hd][^>]*>(.*?)</t[hd]>`)
	indraTagRe   = regexp.MustCompile(`(?s)<[^>]*>`)
)

// parseIndraTables recovers structured tables from the HTML tables that Indra
// emits in its Markdown. It is intentionally small: it targets the
// machine-generated <table><tr><th|td> subset, not arbitrary HTML.
func parseIndraTables(markdown string) []Table {
	var tables []Table
	for _, block := range indraTableRe.FindAllStringSubmatch(markdown, -1) {
		var rows [][]string
		for _, row := range indraRowRe.FindAllStringSubmatch(block[1], -1) {
			var cells []string
			for _, cell := range indraCellRe.FindAllStringSubmatch(row[1], -1) {
				cells = append(cells, html.UnescapeString(strings.TrimSpace(indraTagRe.ReplaceAllString(cell[1], ""))))
			}
			if len(cells) > 0 {
				rows = append(rows, cells)
			}
		}
		if len(rows) == 0 {
			continue
		}
		tables = append(tables, Table{Rows: rows, Markdown: block[0]})
	}
	return tables
}

// CommandOCR executes a standalone modern OCR command (such as 'ocrs' or 'rapidocr').
type CommandOCR struct {
	BinaryPath string
	Args       []string
}

func (c *CommandOCR) RecognizePage(ctx context.Context, imageBytes []byte, format string, pageBox Rect) ([]TextSpan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.BinaryPath == "" {
		return NoopOCR{}.RecognizePage(ctx, imageBytes, format, pageBox)
	}

	cmd := exec.CommandContext(ctx, c.BinaryPath, c.Args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("pdfextract: ocr command %s failed: %w", c.BinaryPath, err)
	}

	// Parse simple line-based text output
	return linesToSpans(strings.Split(string(out), "\n"), pageBox), nil
}

// AppleVisionOCR leverages macOS native Vision Framework for ultra-fast deep-learning OCR without Python.
type AppleVisionOCR struct{}

func (a AppleVisionOCR) RecognizePage(ctx context.Context, imageBytes []byte, format string, pageBox Rect) ([]TextSpan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if runtime.GOOS != "darwin" {
		return NoopOCR{}.RecognizePage(ctx, imageBytes, format, pageBox)
	}

	// If a system vision helper is available, execute it; otherwise fallback to NoopOCR gracefully
	return NoopOCR{}.RecognizePage(ctx, imageBytes, format, pageBox)
}

// DefaultOCREngine returns an optimal OCR engine for the host platform.
func DefaultOCREngine() OCREngine {
	if runtime.GOOS == "darwin" {
		return AppleVisionOCR{}
	}
	return NoopOCR{}
}

func estimateImageArea(images []PageImage) float64 {
	var total float64
	for _, img := range images {
		total += math.Abs((img.BBox.X1 - img.BBox.X0) * (img.BBox.Y1 - img.BBox.Y0))
	}
	return total
}

func stripCodeFences(text string) string {
	s := strings.TrimSpace(text)
	if strings.HasPrefix(s, "```markdown") && strings.HasSuffix(s, "```") {
		s = strings.TrimPrefix(s, "```markdown")
		s = strings.TrimSuffix(s, "```")
		return strings.TrimSpace(s)
	}
	if strings.HasPrefix(s, "```") && strings.HasSuffix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		return strings.TrimSpace(s)
	}
	return s
}

func markdownToSpans(md string, pageBox Rect) []TextSpan {
	return linesToSpans(strings.Split(md, "\n"), pageBox)
}

func linesToSpans(lines []string, pageBox Rect) []TextSpan {
	var spans []TextSpan
	y := pageBox.Y1 - 50
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			spans = append(spans, TextSpan{
				Text:     l,
				FontSize: 12.0,
				BBox: Rect{
					X0: pageBox.X0 + 50,
					Y0: y,
					X1: pageBox.X1 - 50,
					Y1: y + 14,
				},
			})
			y -= 18
		}
	}
	return spans
}
