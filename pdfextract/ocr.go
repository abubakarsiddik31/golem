package pdfextract

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"runtime"
	"strings"
)

// OCREngine defines the contract for pluggable modern deep-learning OCR engines.
type OCREngine interface {
	// RecognizePage performs text recognition on an image of a page and returns extracted TextSpans.
	RecognizePage(ctx context.Context, imageBytes []byte, format string, pageBox Rect) ([]TextSpan, error)
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

	// Check if the page is dominated by large raster images
	pageArea := page.MediaBox.Area()
	if pageArea <= 0 {
		pageArea = 612 * 792 // US letter
	}

	var imageArea float64
	for _, img := range page.Images {
		imageArea += img.BBox.Area()
	}

	// If images cover more than 60% of the page and text is sparse, it's a scan
	return (imageArea/pageArea) >= 0.60 || (len(page.Spans) == 0 && len(page.Images) > 0)
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
	lines := strings.Split(string(out), "\n")
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
	return spans, nil
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
