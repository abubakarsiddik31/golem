package pdfextract_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/pdfextract"
)

// countingMarkdownOCR is an offline OCREngine that returns fixed Markdown and
// tables, counting how many times either recognition entrypoint ran.
type countingMarkdownOCR struct {
	md     string
	tables []pdfextract.Table
	calls  int
}

func (c *countingMarkdownOCR) RecognizePage(context.Context, []byte, string, pdfextract.Rect) ([]pdfextract.TextSpan, error) {
	c.calls++
	return nil, nil
}

func (c *countingMarkdownOCR) RecognizeMarkdown(context.Context, []byte, string, pdfextract.Rect) (pdfextract.MarkdownResult, error) {
	c.calls++
	return pdfextract.MarkdownResult{Markdown: c.md, Tables: c.tables}, nil
}

// countingSpanOCR is an OCREngine that only returns TextSpans, exercising the
// span path rather than the MarkdownRecognizer path.
type countingSpanOCR struct {
	spans []pdfextract.TextSpan
	calls int
}

func (c *countingSpanOCR) RecognizePage(context.Context, []byte, string, pdfextract.Rect) ([]pdfextract.TextSpan, error) {
	c.calls++
	return c.spans, nil
}

// buildTextWithImagePDF constructs a page with both a usable text layer and a
// full-page raster, the case where force-ocr and hybrid provenance apply.
func buildTextWithImagePDF() []byte {
	contentStr := "BT /F1 12 Tf 72 700 Td (This page has a real digital text layer with plenty of characters.) Tj ET\n" +
		"q 612 0 0 792 0 0 cm /Im1 Do Q\n"
	objects := []string{
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>/XObject<</Im1 6 0 R>>>>>>",
		fmt.Sprintf("<</Length %d>>\nstream\n%sendstream", len(contentStr), contentStr),
		"<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>",
		fmt.Sprintf("<</Type/XObject/Subtype/Image/Width 1/Height 1/ColorSpace/DeviceRGB/BitsPerComponent 8/Length %d>>\nstream\n%s\nendstream", 3, string([]byte{220, 230, 240})),
	}

	var sb strings.Builder
	sb.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, obj := range objects {
		offsets[i] = sb.Len()
		fmt.Fprintf(&sb, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xrefPos := sb.Len()
	fmt.Fprintf(&sb, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&sb, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&sb, "trailer\n<</Size %d/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xrefPos)
	return []byte(sb.String())
}

const digitalPageContent = "BT /F1 12 Tf 72 700 Td (This page has a genuine embedded text layer with plenty of text.) Tj ET"

func TestPageSourceDigital(t *testing.T) {
	doc, err := pdfextract.ExtractBytes(context.Background(), buildSimplePDF(digitalPageContent), pdfextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}
	if got := doc.Pages[0].Source; got != pdfextract.PageSourceDigital {
		t.Errorf("Source = %q, want %q", got, pdfextract.PageSourceDigital)
	}
	if doc.Pages[0].IsScanned {
		t.Errorf("IsScanned = true for a digital page")
	}
}

func TestPageSourceOCR(t *testing.T) {
	engine := &countingMarkdownOCR{md: "# Scanned heading\n\nBody text from OCR."}
	doc, err := pdfextract.ExtractBytes(context.Background(), buildScannedPDF(), pdfextract.Options{OCREngine: engine})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}
	if got := doc.Pages[0].Source; got != pdfextract.PageSourceOCR {
		t.Errorf("Source = %q, want %q", got, pdfextract.PageSourceOCR)
	}
	if !doc.Pages[0].IsScanned {
		t.Errorf("IsScanned = false for a scanned page")
	}
	if engine.calls != 1 {
		t.Errorf("OCR calls = %d, want 1", engine.calls)
	}
}

func TestPageSourceEmptyWithoutEngine(t *testing.T) {
	doc, err := pdfextract.ExtractBytes(context.Background(), buildScannedPDF(), pdfextract.Options{DisableOCR: true})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}
	if got := doc.Pages[0].Source; got != pdfextract.PageSourceEmpty {
		t.Errorf("Source = %q, want %q", got, pdfextract.PageSourceEmpty)
	}
}

func TestPageSourceHybrid(t *testing.T) {
	engine := &countingSpanOCR{spans: []pdfextract.TextSpan{{
		Text:     "OCR line",
		FontSize: 12,
		BBox:     pdfextract.Rect{X0: 72, Y0: 300, X1: 200, Y1: 320},
	}}}
	doc, err := pdfextract.ExtractBytes(context.Background(), buildTextWithImagePDF(), pdfextract.Options{
		OCREngine: engine,
		Mode:      pdfextract.ModeForceOCR,
	})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}
	if got := doc.Pages[0].Source; got != pdfextract.PageSourceHybrid {
		t.Errorf("Source = %q, want %q", got, pdfextract.PageSourceHybrid)
	}
}

func TestModeAutoSkipsOCRForDigitalPage(t *testing.T) {
	engine := &countingMarkdownOCR{md: "should not be used"}
	doc, err := pdfextract.ExtractBytes(context.Background(), buildSimplePDF(digitalPageContent), pdfextract.Options{OCREngine: engine})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}
	if engine.calls != 0 {
		t.Errorf("OCR calls = %d, want 0 for a digital page in auto mode", engine.calls)
	}
	if !strings.Contains(doc.Markdown, "genuine embedded text layer") {
		t.Errorf("digital text missing from Markdown:\n%s", doc.Markdown)
	}
}

func TestModePreferTextSkipsOCRForScan(t *testing.T) {
	engine := &countingMarkdownOCR{md: "should not be used"}
	doc, err := pdfextract.ExtractBytes(context.Background(), buildScannedPDF(), pdfextract.Options{
		OCREngine: engine,
		Mode:      pdfextract.ModePreferText,
	})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}
	if engine.calls != 0 {
		t.Errorf("OCR calls = %d, want 0 in prefer-text mode", engine.calls)
	}
	// Detection is independent of routing: the page still looks scanned.
	if !doc.Pages[0].IsScanned {
		t.Errorf("IsScanned = false; detection must not depend on the routing mode")
	}
	if got := doc.Pages[0].Source; got != pdfextract.PageSourceEmpty {
		t.Errorf("Source = %q, want %q", got, pdfextract.PageSourceEmpty)
	}
	if !strings.Contains(doc.Markdown, "OCR engine not configured") {
		t.Errorf("expected the not-configured placeholder, got:\n%s", doc.Markdown)
	}
}

func TestModeForceOCRReplacesTextLayer(t *testing.T) {
	engine := &countingMarkdownOCR{md: "# OCR override\n\nTranscribed from the raster."}
	doc, err := pdfextract.ExtractBytes(context.Background(), buildTextWithImagePDF(), pdfextract.Options{
		OCREngine: engine,
		Mode:      pdfextract.ModeForceOCR,
	})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}
	if engine.calls != 1 {
		t.Errorf("OCR calls = %d, want 1 in force-ocr mode", engine.calls)
	}
	if !strings.Contains(doc.Markdown, "# OCR override") {
		t.Errorf("force-ocr Markdown missing:\n%s", doc.Markdown)
	}
	if strings.Contains(doc.Markdown, "genuine embedded text layer") {
		t.Errorf("force-ocr must discard the text layer, got:\n%s", doc.Markdown)
	}
	if got := doc.Pages[0].Source; got != pdfextract.PageSourceOCR {
		t.Errorf("Source = %q, want %q", got, pdfextract.PageSourceOCR)
	}
}

func TestModeForceOCRWithoutImageFallsBackToText(t *testing.T) {
	engine := &countingMarkdownOCR{md: "should not be used"}
	doc, err := pdfextract.ExtractBytes(context.Background(), buildSimplePDF(digitalPageContent), pdfextract.Options{
		OCREngine: engine,
		Mode:      pdfextract.ModeForceOCR,
	})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}
	if engine.calls != 0 {
		t.Errorf("OCR calls = %d, want 0 when no page image is available", engine.calls)
	}
	if got := doc.Pages[0].Source; got != pdfextract.PageSourceDigital {
		t.Errorf("Source = %q, want %q", got, pdfextract.PageSourceDigital)
	}
}

func TestDisableOCRBeatsForceMode(t *testing.T) {
	engine := &countingMarkdownOCR{md: "should not be used"}
	doc, err := pdfextract.ExtractBytes(context.Background(), buildScannedPDF(), pdfextract.Options{
		OCREngine:  engine,
		Mode:       pdfextract.ModeForceOCR,
		DisableOCR: true,
	})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}
	if engine.calls != 0 {
		t.Errorf("OCR calls = %d, want 0 when DisableOCR is set", engine.calls)
	}
	if got := doc.Pages[0].Source; got != pdfextract.PageSourceEmpty {
		t.Errorf("Source = %q, want %q", got, pdfextract.PageSourceEmpty)
	}
}

func TestExtractScannedPagePopulatesTables(t *testing.T) {
	engine := &countingMarkdownOCR{
		md: "# Invoice\n\n<table><tr><th>Item</th><th>Qty</th></tr><tr><td>Coffee</td><td>3</td></tr></table>",
		tables: []pdfextract.Table{
			{Rows: [][]string{{"Item", "Qty"}, {"Coffee", "3"}}, Markdown: "<table>...</table>"},
		},
	}
	doc, err := pdfextract.ExtractBytes(context.Background(), buildScannedPDF(), pdfextract.Options{OCREngine: engine})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}
	if len(doc.Tables) != 1 {
		t.Fatalf("Document.Tables = %d, want 1", len(doc.Tables))
	}
	if doc.Tables[0].Page != 0 {
		t.Errorf("Table.Page = %d, want 0", doc.Tables[0].Page)
	}
	if len(doc.Pages[0].Tables) != 1 {
		t.Errorf("Page.Tables = %d, want 1", len(doc.Pages[0].Tables))
	}
	if got := doc.Tables[0].Rows[1][0]; got != "Coffee" {
		t.Errorf("Table cell = %q, want %q", got, "Coffee")
	}
}

func TestNewRejectsUnknownOCRMode(t *testing.T) {
	dir := t.TempDir()
	_, err := pdfextract.New[struct{}](pdfextract.Config{Root: dir, Mode: pdfextract.OCRMode("sometimes")})
	if err == nil {
		t.Fatalf("New accepted an unknown OCR mode")
	}
	if !strings.Contains(err.Error(), "unknown OCR mode") {
		t.Errorf("error = %v, want an unknown-OCR-mode message", err)
	}
}
