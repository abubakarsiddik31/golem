package pdfextract_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/pdfextract"
)

// buildSimplePDF crafts a minimal valid PDF-1.4 file with custom content streams.
func buildSimplePDF(contentStream string) []byte {
	return buildMultiPagePDF([]string{contentStream})
}

// buildMultiPagePDF constructs a valid multi-page PDF document.
func buildMultiPagePDF(pages []string) []byte {
	var objects []string

	// Object 1: Catalog
	objects = append(objects, "<</Type/Catalog/Pages 2 0 R>>")

	// Object 2: Pages tree
	kids := make([]string, len(pages))
	for i := range pages {
		kids[i] = fmt.Sprintf("%d 0 R", 3+i*3)
	}
	objects = append(objects, fmt.Sprintf("<</Type/Pages/Kids[%s]/Count %d>>", strings.Join(kids, " "), len(pages)))

	// Build each page: Page (3+i*3), Contents (4+i*3), Font (5+i*3)
	for i, pageContent := range pages {
		contentObjNum := 4 + i*3
		fontObjNum := 5 + i*3

		pageObj := fmt.Sprintf("<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents %d 0 R/Resources<</Font<</F1 %d 0 R>>>>>>", contentObjNum, fontObjNum)
		contentObj := fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(pageContent), pageContent)
		fontObj := "<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>"

		objects = append(objects, pageObj, contentObj, fontObj)
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

func TestExtractSimpleText(t *testing.T) {
	content := "BT /F1 12 Tf 100 700 Td (Hello from Golem PDF extract!) Tj ET"
	pdfBytes := buildSimplePDF(content)

	doc, err := pdfextract.ExtractBytes(context.Background(), pdfBytes, pdfextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}

	if doc.NumPages != 1 {
		t.Fatalf("NumPages = %d, want 1", doc.NumPages)
	}
	if !strings.Contains(doc.Markdown, "Hello from Golem PDF extract!") {
		t.Fatalf("Markdown does not contain expected text, got:\n%s", doc.Markdown)
	}
}

func TestExtractMultiColumnReadingOrder(t *testing.T) {
	// 2-column layout:
	// Left column (X=60):
	//   Line 1 at Y=700
	//   Line 2 at Y=650
	// Right column (X=360):
	//   Line 1 at Y=700
	//   Line 2 at Y=650
	var sb strings.Builder
	sb.WriteString("BT /F1 12 Tf ")
	sb.WriteString("60 700 Td (Left Column Top) Tj ")
	sb.WriteString("0 -50 Td (Left Column Bottom) Tj ")
	sb.WriteString("300 50 Td (Right Column Top) Tj ")
	sb.WriteString("0 -50 Td (Right Column Bottom) Tj ")
	sb.WriteString("ET")

	pdfBytes := buildSimplePDF(sb.String())
	doc, err := pdfextract.ExtractBytes(context.Background(), pdfBytes, pdfextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}

	md := doc.Markdown
	leftTopIdx := strings.Index(md, "Left Column Top")
	leftBottomIdx := strings.Index(md, "Left Column Bottom")
	rightTopIdx := strings.Index(md, "Right Column Top")
	rightBottomIdx := strings.Index(md, "Right Column Bottom")

	if leftTopIdx < 0 || leftBottomIdx < 0 || rightTopIdx < 0 || rightBottomIdx < 0 {
		t.Fatalf("Not all column strings found in markdown:\n%s", md)
	}

	// Verify reading order: Left Column Top -> Left Column Bottom -> Right Column Top -> Right Column Bottom
	if !(leftTopIdx < leftBottomIdx && leftBottomIdx < rightTopIdx && rightTopIdx < rightBottomIdx) {
		t.Fatalf("Column reading order incorrect! Expected Left column before Right column. Got indices: leftTop=%d, leftBottom=%d, rightTop=%d, rightBottom=%d\nMarkdown:\n%s",
			leftTopIdx, leftBottomIdx, rightTopIdx, rightBottomIdx, md)
	}
}

func TestExtractHeadingsHierarchy(t *testing.T) {
	var sb strings.Builder
	// Title (24pt)
	sb.WriteString("BT /F1 24 Tf 100 720 Td (Document Main Title) Tj ET\n")
	// Section (16pt)
	sb.WriteString("BT /F1 16 Tf 100 680 Td (Section Header) Tj ET\n")
	// Body Paragraph (12pt)
	sb.WriteString("BT /F1 12 Tf 100 640 Td (This is standard body paragraph text.) Tj ET\n")

	pdfBytes := buildSimplePDF(sb.String())
	doc, err := pdfextract.ExtractBytes(context.Background(), pdfBytes, pdfextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}

	md := doc.Markdown
	if !strings.Contains(md, "# Document Main Title") {
		t.Errorf("Expected '# Document Main Title', got:\n%s", md)
	}
	if !strings.Contains(md, "## Section Header") {
		t.Errorf("Expected '## Section Header', got:\n%s", md)
	}
	if !strings.Contains(md, "This is standard body paragraph text.") {
		t.Errorf("Expected body paragraph text, got:\n%s", md)
	}
}

func TestExtractRuledTableLattice(t *testing.T) {
	// Construct a PDF with a 2x2 bordered table using vector rectangle and line strokes:
	// Top border: y = 500, x = 100 to 300
	// Middle border: y = 475, x = 100 to 300
	// Bottom border: y = 450, x = 100 to 300
	// Left border: x = 100, y = 450 to 500
	// Center border: x = 200, y = 450 to 500
	// Right border: x = 300, y = 450 to 500
	var sb strings.Builder
	// Draw horizontal lines
	sb.WriteString("100 500 m 300 500 l S\n")
	sb.WriteString("100 475 m 300 475 l S\n")
	sb.WriteString("100 450 m 300 450 l S\n")
	// Draw vertical lines
	sb.WriteString("100 450 m 100 500 l S\n")
	sb.WriteString("200 450 m 200 500 l S\n")
	sb.WriteString("300 450 m 300 500 l S\n")

	// Text in cells:
	// Row 0: Cell (0,0) at (110, 485): "Item", Cell (0,1) at (210, 485): "Price"
	// Row 1: Cell (1,0) at (110, 460): "Widget", Cell (1,1) at (210, 460): "$19.99"
	sb.WriteString("BT /F1 10 Tf 110 485 Td (Item) Tj ET\n")
	sb.WriteString("BT /F1 10 Tf 210 485 Td (Price) Tj ET\n")
	sb.WriteString("BT /F1 10 Tf 110 460 Td (Widget) Tj ET\n")
	sb.WriteString("BT /F1 10 Tf 210 460 Td ($19.99) Tj ET\n")

	pdfBytes := buildSimplePDF(sb.String())
	doc, err := pdfextract.ExtractBytes(context.Background(), pdfBytes, pdfextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}

	if len(doc.Tables) == 0 {
		t.Fatalf("Expected at least 1 table extracted, got 0. Markdown:\n%s", doc.Markdown)
	}

	tableMD := doc.Tables[0].Markdown
	if !strings.Contains(tableMD, "Item") || !strings.Contains(tableMD, "Price") ||
		!strings.Contains(tableMD, "Widget") || !strings.Contains(tableMD, "$19.99") {
		t.Fatalf("Table markdown missing expected cells:\n%s", tableMD)
	}

	// Verify markdown table formatting structure
	if !strings.Contains(tableMD, "| Item") || !strings.Contains(tableMD, "|:---") {
		t.Fatalf("Table markdown is not proper GFM table:\n%s", tableMD)
	}
}

func TestRejectEmptyTableGrid(t *testing.T) {
	// Construct a PDF with a vector line grid (e.g. 3x3 cells) representing a chart/diagram
	// with NO text inside any cell.
	var sb strings.Builder
	// Horizontal lines
	sb.WriteString("100 500 m 300 500 l S\n")
	sb.WriteString("100 475 m 300 475 l S\n")
	sb.WriteString("100 450 m 300 450 l S\n")
	sb.WriteString("100 425 m 300 425 l S\n")
	// Vertical lines
	sb.WriteString("100 425 m 100 500 l S\n")
	sb.WriteString("166 425 m 166 500 l S\n")
	sb.WriteString("233 425 m 233 500 l S\n")
	sb.WriteString("300 425 m 300 500 l S\n")

	// Only external text outside the grid
	sb.WriteString("BT /F1 10 Tf 100 380 Td (Figure 1: Chart description) Tj ET\n")

	pdfBytes := buildSimplePDF(sb.String())
	doc, err := pdfextract.ExtractBytes(context.Background(), pdfBytes, pdfextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}

	if len(doc.Tables) != 0 {
		t.Fatalf("Expected 0 tables for empty vector grid, got %d. Markdown:\n%s", len(doc.Tables), doc.Markdown)
	}
	if strings.Contains(doc.Markdown, "| Col 1") || strings.Contains(doc.Markdown, "|:---") {
		t.Fatalf("Markdown should not contain empty table markup, got:\n%s", doc.Markdown)
	}
	if !strings.Contains(doc.Markdown, "Figure 1: Chart description") {
		t.Fatalf("External text should still be present, got:\n%s", doc.Markdown)
	}
}

func TestRejectSingleRowFalsePositiveTable(t *testing.T) {
	// Construct a PDF where a single sentence (e.g. Theorem statement) is boxed by lines
	var sb strings.Builder
	// Horizontal lines
	sb.WriteString("100 500 m 400 500 l S\n")
	sb.WriteString("100 470 m 400 470 l S\n")
	sb.WriteString("100 440 m 400 440 l S\n")
	// Vertical lines
	sb.WriteString("100 440 m 100 500 l S\n")
	sb.WriteString("250 440 m 250 500 l S\n")
	sb.WriteString("400 440 m 400 500 l S\n")

	// Text only in row 0, row 1 is completely empty
	sb.WriteString("BT /F1 10 Tf 110 480 Td (Theorem 1) Tj ET\n")
	sb.WriteString("BT /F1 10 Tf 260 480 Td (Given X = Y) Tj ET\n")

	pdfBytes := buildSimplePDF(sb.String())
	doc, err := pdfextract.ExtractBytes(context.Background(), pdfBytes, pdfextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}

	if len(doc.Tables) != 0 {
		t.Fatalf("Expected 0 tables for single-row boxed text, got %d", len(doc.Tables))
	}
	// The theorem text should NOT be lost or swallowed
	if !strings.Contains(doc.Markdown, "Theorem 1") || !strings.Contains(doc.Markdown, "Given X = Y") {
		t.Fatalf("Text inside rejected single-row box must be retained as normal text, got:\n%s", doc.Markdown)
	}
}

func TestPrunePhantomEmptyColumns(t *testing.T) {
	// 3-column grid where middle column has no text in any row
	var sb strings.Builder
	// Horizontal lines
	sb.WriteString("100 500 m 400 500 l S\n")
	sb.WriteString("100 470 m 400 470 l S\n")
	sb.WriteString("100 440 m 400 440 l S\n")
	// Vertical lines: 100, 200, 300, 400
	sb.WriteString("100 440 m 100 500 l S\n")
	sb.WriteString("200 440 m 200 500 l S\n")
	sb.WriteString("300 440 m 300 500 l S\n")
	sb.WriteString("400 440 m 400 500 l S\n")

	// Text in Col 0 and Col 2, but Col 1 has NO text
	sb.WriteString("BT /F1 10 Tf 110 480 Td (Header A) Tj ET\n")
	sb.WriteString("BT /F1 10 Tf 310 480 Td (Header B) Tj ET\n")
	sb.WriteString("BT /F1 10 Tf 110 450 Td (Val A) Tj ET\n")
	sb.WriteString("BT /F1 10 Tf 310 450 Td (Val B) Tj ET\n")

	pdfBytes := buildSimplePDF(sb.String())
	doc, err := pdfextract.ExtractBytes(context.Background(), pdfBytes, pdfextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}

	if len(doc.Tables) != 1 {
		t.Fatalf("Expected 1 table, got %d", len(doc.Tables))
	}
	tbl := doc.Tables[0]
	if len(tbl.Rows[0]) != 2 {
		t.Fatalf("Expected 2 columns after pruning empty column, got %d: %v", len(tbl.Rows[0]), tbl.Rows[0])
	}
	if strings.Contains(tbl.Markdown, "Col 2") {
		t.Fatalf("Markdown table should not contain dummy fallback header for pruned column, got:\n%s", tbl.Markdown)
	}
}

func TestExtractBooktabsTable(t *testing.T) {
	// Construct a PDF with horizontal rules only (no vertical lines):
	// Top rule at Y=500, mid rule at Y=475, bottom rule at Y=400
	var sb strings.Builder
	// Horizontal lines
	sb.WriteString("100 500 m 400 500 l S\n")
	sb.WriteString("100 475 m 400 475 l S\n")
	sb.WriteString("100 400 m 400 400 l S\n")

	// Header row between 500 and 475
	sb.WriteString("BT /F1 10 Tf 110 485 Td (Model) Tj ET\n")
	sb.WriteString("BT /F1 10 Tf 210 485 Td (Accuracy) Tj ET\n")
	sb.WriteString("BT /F1 10 Tf 310 485 Td (Latency) Tj ET\n")

	// Data rows between 475 and 400
	sb.WriteString("BT /F1 10 Tf 110 450 Td (GPT-4) Tj ET\n")
	sb.WriteString("BT /F1 10 Tf 210 450 Td (92.5%) Tj ET\n")
	sb.WriteString("BT /F1 10 Tf 310 450 Td (120ms) Tj ET\n")

	sb.WriteString("BT /F1 10 Tf 110 420 Td (Claude-3) Tj ET\n")
	sb.WriteString("BT /F1 10 Tf 210 420 Td (93.1%) Tj ET\n")
	sb.WriteString("BT /F1 10 Tf 310 420 Td (95ms) Tj ET\n")

	pdfBytes := buildSimplePDF(sb.String())
	doc, err := pdfextract.ExtractBytes(context.Background(), pdfBytes, pdfextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}

	if len(doc.Tables) != 1 {
		t.Fatalf("Expected 1 booktabs table, got %d. Markdown:\n%s", len(doc.Tables), doc.Markdown)
	}
	tbl := doc.Tables[0]
	if len(tbl.Rows) != 3 || len(tbl.Rows[0]) != 3 {
		t.Fatalf("Expected 3x3 table, got %dx%d: %v", len(tbl.Rows), len(tbl.Rows[0]), tbl.Rows)
	}
	if tbl.Rows[0][0] != "Model" || tbl.Rows[0][1] != "Accuracy" || tbl.Rows[0][2] != "Latency" {
		t.Fatalf("Header row incorrect, got: %v", tbl.Rows[0])
	}
	if tbl.Rows[1][0] != "GPT-4" || tbl.Rows[2][0] != "Claude-3" {
		t.Fatalf("Data rows incorrect, got: %v", tbl.Rows)
	}
}

func TestSparseTableAccepted(t *testing.T) {
	// 4x3 table where some cells are empty (sparse data)
	var sb strings.Builder
	sb.WriteString("100 500 m 400 500 l S\n")
	sb.WriteString("100 475 m 400 475 l S\n")
	sb.WriteString("100 380 m 400 380 l S\n")

	// Header row
	sb.WriteString("BT /F1 10 Tf 110 485 Td (Feature) Tj ET\n")
	sb.WriteString("BT /F1 10 Tf 210 485 Td (Basic) Tj ET\n")
	sb.WriteString("BT /F1 10 Tf 310 485 Td (Pro) Tj ET\n")

	// Row 1: only Pro has value
	sb.WriteString("BT /F1 10 Tf 110 450 Td (SSO) Tj ET\n")
	sb.WriteString("BT /F1 10 Tf 310 450 Td (Yes) Tj ET\n")

	// Row 2: only Basic has value
	sb.WriteString("BT /F1 10 Tf 110 420 Td (Free Trial) Tj ET\n")
	sb.WriteString("BT /F1 10 Tf 210 420 Td (Yes) Tj ET\n")

	// Row 3: both have values
	sb.WriteString("BT /F1 10 Tf 110 395 Td (Support) Tj ET\n")
	sb.WriteString("BT /F1 10 Tf 210 395 Td (Email) Tj ET\n")
	sb.WriteString("BT /F1 10 Tf 310 395 Td (24/7) Tj ET\n")

	pdfBytes := buildSimplePDF(sb.String())
	doc, err := pdfextract.ExtractBytes(context.Background(), pdfBytes, pdfextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}

	if len(doc.Tables) != 1 {
		t.Fatalf("Expected 1 sparse table accepted, got %d", len(doc.Tables))
	}
	tbl := doc.Tables[0]
	if len(tbl.Rows) != 4 || len(tbl.Rows[0]) != 3 {
		t.Fatalf("Expected 4x3 table, got %dx%d: %v", len(tbl.Rows), len(tbl.Rows[0]), tbl.Rows)
	}
}

func TestExtractImageWithCaptionProximity(t *testing.T) {
	// Build a PDF with an image XObject and adjacent caption text
	// 1 0 obj: Catalog
	// 2 0 obj: Pages
	// 3 0 obj: Page
	// 4 0 obj: Content
	// 5 0 obj: Font
	// 6 0 obj: Image XObject
	var objects []string
	objects = append(objects, "<</Type/Catalog/Pages 2 0 R>>")
	objects = append(objects, "<</Type/Pages/Kids[3 0 R]/Count 1>>")
	objects = append(objects, "<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>/XObject<</Im1 6 0 R>>>>>>")

	// Content stream:
	// 1. Invokes image Im1 scaled to 200x150 placed at (100, 450)
	// 2. Draws caption text directly below image at (100, 425): "Figure 1: High level architecture"
	var content strings.Builder
	content.WriteString("q 200 0 0 150 100 450 cm /Im1 Do Q\n")
	content.WriteString("BT /F1 10 Tf 100 425 Td (Figure 1: High level architecture) Tj ET\n")
	objects = append(objects, fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", content.Len(), content.String()))

	// Font object
	objects = append(objects, "<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>")

	// Image XObject (1x1 dummy raw sample)
	imgData := []byte{255, 0, 0} // 1 red pixel
	objects = append(objects, fmt.Sprintf("<</Type/XObject/Subtype/Image/Width 1/Height 1/ColorSpace/DeviceRGB/BitsPerComponent 8/Length %d>>\nstream\n%s\nendstream", len(imgData), string(imgData)))

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

	pdfBytes := []byte(sb.String())

	doc, err := pdfextract.ExtractBytes(context.Background(), pdfBytes, pdfextract.Options{
		ReturnImageParts: true,
	})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}

	if len(doc.Images) == 0 {
		t.Fatalf("Expected 1 image extracted, got 0")
	}

	img := doc.Images[0]
	if !strings.Contains(img.Caption, "Figure 1: High level architecture") {
		t.Fatalf("Image caption = %q, want 'Figure 1: High level architecture'", img.Caption)
	}

	// Verify markdown contains placeholder with description
	if !strings.Contains(doc.Markdown, "![Figure 1: High level architecture]") {
		t.Fatalf("Markdown does not contain image placeholder with caption:\n%s", doc.Markdown)
	}
}

func TestExtractMultiPage(t *testing.T) {
	page1 := "BT /F1 12 Tf 100 700 Td (Content on Page 1) Tj ET"
	page2 := "BT /F1 12 Tf 100 700 Td (Content on Page 2) Tj ET"
	pdfBytes := buildMultiPagePDF([]string{page1, page2})

	doc, err := pdfextract.ExtractBytes(context.Background(), pdfBytes, pdfextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}

	if doc.NumPages != 2 {
		t.Fatalf("NumPages = %d, want 2", doc.NumPages)
	}
	if !strings.Contains(doc.Markdown, "## Page 1") || !strings.Contains(doc.Markdown, "## Page 2") {
		t.Fatalf("Multi-page Markdown missing page headers:\n%s", doc.Markdown)
	}
	if !strings.Contains(doc.Markdown, "Content on Page 1") || !strings.Contains(doc.Markdown, "Content on Page 2") {
		t.Fatalf("Markdown missing page texts:\n%s", doc.Markdown)
	}
}

func TestExtractPageRangeOption(t *testing.T) {
	page1 := "BT /F1 12 Tf 100 700 Td (First Page Text) Tj ET"
	page2 := "BT /F1 12 Tf 100 700 Td (Second Page Text) Tj ET"
	page3 := "BT /F1 12 Tf 100 700 Td (Third Page Text) Tj ET"
	pdfBytes := buildMultiPagePDF([]string{page1, page2, page3})

	// Request page 2 only
	doc, err := pdfextract.ExtractBytes(context.Background(), pdfBytes, pdfextract.Options{
		Pages: "2",
	})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}

	if strings.Contains(doc.Markdown, "First Page Text") {
		t.Errorf("Markdown contains First Page Text when only page 2 was requested")
	}
	if !strings.Contains(doc.Markdown, "Second Page Text") {
		t.Errorf("Markdown missing Second Page Text")
	}
	if strings.Contains(doc.Markdown, "Third Page Text") {
		t.Errorf("Markdown contains Third Page Text when only page 2 was requested")
	}
}

func TestToolConstructorValidation(t *testing.T) {
	tmpDir := t.TempDir()

	// Empty root
	_, err := pdfextract.New[struct{}](pdfextract.Config{Root: ""})
	if err == nil {
		t.Fatal("New with empty root should fail")
	}

	// Negative max bytes
	_, err = pdfextract.New[struct{}](pdfextract.Config{Root: tmpDir, MaxBytes: -1})
	if err == nil {
		t.Fatal("New with negative MaxBytes should fail")
	}

	// Non-existent root
	_, err = pdfextract.New[struct{}](pdfextract.Config{Root: filepath.Join(tmpDir, "missing")})
	if err == nil {
		t.Fatal("New with non-existent root should fail")
	}

	// Valid config
	toolInstance, err := pdfextract.New[struct{}](pdfextract.Config{Root: tmpDir})
	if err != nil {
		t.Fatalf("New with valid config failed: %v", err)
	}
	if toolInstance.Name != pdfextract.ToolName {
		t.Fatalf("tool name = %q, want %q", toolInstance.Name, pdfextract.ToolName)
	}
}

func TestToolExecution(t *testing.T) {
	tmpDir := t.TempDir()
	pdfContent := buildSimplePDF("BT /F1 12 Tf 100 700 Td (Secret report content) Tj ET")
	pdfFile := filepath.Join(tmpDir, "report.pdf")
	if err := os.WriteFile(pdfFile, pdfContent, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	toolInstance := pdfextract.MustNew[struct{}](pdfextract.Config{Root: tmpDir})

	ctx := context.Background()

	// 1. Success execution
	res, err := toolInstance.Exec(ctx, struct{}{}, json.RawMessage(`{"path": "report.pdf"}`))
	if err != nil {
		t.Fatalf("tool Exec failed: %v", err)
	}
	if !strings.Contains(res.Text, "Secret report content") {
		t.Fatalf("tool result text = %q, want contains 'Secret report content'", res.Text)
	}

	// 2. Traversal attempt rejects as correctable *model.ModelRetry
	_, err = toolInstance.Exec(ctx, struct{}{}, json.RawMessage(`{"path": "../secret.pdf"}`))
	if err == nil {
		t.Fatal("Expected error on path traversal")
	}
	var retry *model.ModelRetry
	if !errors.As(err, &retry) {
		t.Fatalf("Expected *model.ModelRetry on traversal, got: %T (%v)", err, err)
	}

	// 3. Absolute path rejects as *model.ModelRetry
	_, err = toolInstance.Exec(ctx, struct{}{}, json.RawMessage(`{"path": "/etc/passwd"}`))
	if !errors.As(err, &retry) {
		t.Fatalf("Expected *model.ModelRetry on absolute path, got: %T (%v)", err, err)
	}

	// 4. Missing file rejects as *model.ModelRetry
	_, err = toolInstance.Exec(ctx, struct{}{}, json.RawMessage(`{"path": "nonexistent.pdf"}`))
	if !errors.As(err, &retry) {
		t.Fatalf("Expected *model.ModelRetry on missing file, got: %T (%v)", err, err)
	}

	// 5. Directory rejects as *model.ModelRetry
	subDir := filepath.Join(tmpDir, "folder")
	_ = os.Mkdir(subDir, 0755)
	_, err = toolInstance.Exec(ctx, struct{}{}, json.RawMessage(`{"path": "folder"}`))
	if !errors.As(err, &retry) {
		t.Fatalf("Expected *model.ModelRetry on directory, got: %T (%v)", err, err)
	}

	// 6. Context cancellation
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = toolInstance.Exec(cancelCtx, struct{}{}, json.RawMessage(`{"path": "report.pdf"}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Expected context.Canceled, got %v", err)
	}
}

func TestToolResultParts(t *testing.T) {
	tmpDir := t.TempDir()

	// PDF with an image XObject
	var objects []string
	objects = append(objects, "<</Type/Catalog/Pages 2 0 R>>")
	objects = append(objects, "<</Type/Pages/Kids[3 0 R]/Count 1>>")
	objects = append(objects, "<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</XObject<</Im1 5 0 R>>>>>>")
	contentStr := "q 100 0 0 100 50 500 cm /Im1 Do Q\n"
	objects = append(objects, fmt.Sprintf("<</Length %d>>\nstream\n%sendstream", len(contentStr), contentStr))
	imgData := []byte{100, 150, 200}
	objects = append(objects, fmt.Sprintf("<</Type/XObject/Subtype/Image/Width 1/Height 1/ColorSpace/DeviceRGB/BitsPerComponent 8/Length %d>>\nstream\n%s\nendstream", len(imgData), string(imgData)))

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

	pdfFile := filepath.Join(tmpDir, "image_doc.pdf")
	if err := os.WriteFile(pdfFile, []byte(sb.String()), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	toolInstance := pdfextract.MustNew[struct{}](pdfextract.Config{
		Root:             tmpDir,
		ReturnImageParts: true,
	})

	res, err := toolInstance.Exec(context.Background(), struct{}{}, json.RawMessage(`{"path": "image_doc.pdf"}`))
	if err != nil {
		t.Fatalf("tool Exec failed: %v", err)
	}

	if len(res.Parts) == 0 {
		t.Fatalf("Expected tool result parts with extracted image, got 0")
	}
	if res.Parts[0].Kind != model.PartImage {
		t.Fatalf("Expected PartImage, got kind %v", res.Parts[0].Kind)
	}
}

func BenchmarkExtract(b *testing.B) {
	// A representative document containing headings, 2 columns, and a ruled table
	var content strings.Builder
	content.WriteString("BT /F1 20 Tf 50 720 Td (Annual Engineering Report) Tj ET\n")
	// Left column
	content.WriteString("BT /F1 12 Tf 50 680 Td (Left column paragraph with detailed architecture notes.) Tj ET\n")
	content.WriteString("BT /F1 12 Tf 50 650 Td (Second line of the left column description.) Tj ET\n")
	// Right column
	content.WriteString("BT /F1 12 Tf 350 680 Td (Right column begins here with performance metrics.) Tj ET\n")
	content.WriteString("BT /F1 12 Tf 350 650 Td (Second line of the right column evaluation.) Tj ET\n")
	// Ruled Table
	content.WriteString("50 550 m 300 550 l S\n50 525 m 300 525 l S\n50 500 m 300 500 l S\n")
	content.WriteString("50 500 m 50 550 l S\n175 500 m 175 550 l S\n300 500 m 300 550 l S\n")
	content.WriteString("BT /F1 10 Tf 60 535 Td (Metric) Tj ET\n")
	content.WriteString("BT /F1 10 Tf 185 535 Td (Score) Tj ET\n")
	content.WriteString("BT /F1 10 Tf 60 510 Td (Latency) Tj ET\n")
	content.WriteString("BT /F1 10 Tf 185 510 Td (4.2ms) Tj ET\n")

	pdfBytes := buildSimplePDF(content.String())
	ctx := context.Background()
	opts := pdfextract.Options{}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		doc, err := pdfextract.ExtractBytes(ctx, pdfBytes, opts)
		if err != nil || len(doc.Markdown) == 0 {
			b.Fatal(err)
		}
	}
}

func TestMathAndUnicodeCleaning(t *testing.T) {
	// PDF with math formula containing sub/superscript and equation tag
	var content strings.Builder
	content.WriteString("BT /F1 12 Tf 1 0 0 1 100 700 Tm (We define the formula as:) Tj ET\n")
	content.WriteString("BT /F1 10 Tf 1 0 0 1 120 660 Tm (f\\(x\\) = x) Tj /F1 7 Tf 1 0 0 1 155 663 Tm (2) Tj /F1 10 Tf 1 0 0 1 162 660 Tm ( + b) Tj /F1 7 Tf 1 0 0 1 180 658 Tm (1) Tj /F1 10 Tf 1 0 0 1 450 660 Tm ( \\(1\\)) Tj ET\n")

	pdfBytes := buildSimplePDF(content.String())
	doc, err := pdfextract.ExtractBytes(context.Background(), pdfBytes, pdfextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}

	md := doc.Markdown
	// Verify superscript ^2 and subscript _1
	if !strings.Contains(md, "^2") {
		t.Errorf("Expected '^2' in extracted math, got:\n%s", md)
	}
	if !strings.Contains(md, "_1") {
		t.Errorf("Expected '_1' in extracted math, got:\n%s", md)
	}
	if !strings.Contains(md, `\tag{1}`) {
		t.Errorf("Expected equation tag '\\tag{1}', got:\n%s", md)
	}
}

func TestCMapSurrogateAndMathNormalization(t *testing.T) {
	// Test CleanText normalization of mathematical symbols
	// U+1D434 is Math Italic A, U+1D707 is Math Greek mu
	raw := "\U0001D434 = \U0001D707 \U0001D440 + \U0001D437"
	cleaned := pdfextract.CleanText(raw)
	expected := "A = μ M + D"
	if cleaned != expected {
		t.Errorf("Expected %q, got %q", expected, cleaned)
	}
}

func TestInheritedPageAttributesAndRotation(t *testing.T) {
	// Craft a PDF where MediaBox, Resources, and Rotate are set on the /Pages parent node
	var objects []string
	objects = append(objects, "<</Type/Catalog/Pages 2 0 R>>")
	objects = append(objects, "<</Type/Pages/Kids[3 0 R]/Count 1/MediaBox[0 0 612 792]/Rotate 90/Resources<</Font<</F1 5 0 R>>>>>>")
	objects = append(objects, "<</Type/Page/Parent 2 0 R/Contents 4 0 R>>") // No MediaBox/Resources on Page!
	content := "BT /F1 14 Tf 1 0 0 1 50 700 Tm (Inherited Title) Tj ET\n"
	objects = append(objects, fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(content), content))
	objects = append(objects, "<</Type/Font/Subtype/Type1/BaseFont/Helvetica/Encoding<</Type/Encoding/Differences[128 /bullet 129 /emdash]>>>>")

	var sb strings.Builder
	sb.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, obj := range objects {
		offsets[i] = sb.Len()
		sb.WriteString(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, obj))
	}
	xrefOffset := sb.Len()
	sb.WriteString(fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objects)+1))
	for _, off := range offsets {
		sb.WriteString(fmt.Sprintf("%010d 00000 n \n", off))
	}
	sb.WriteString(fmt.Sprintf("trailer\n<</Size %d/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xrefOffset))

	doc, err := pdfextract.ExtractBytes(context.Background(), []byte(sb.String()), pdfextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes failed on inherited attributes PDF: %v", err)
	}

	if !strings.Contains(doc.Markdown, "Inherited Title") {
		t.Errorf("Expected 'Inherited Title' in markdown, got:\n%s", doc.Markdown)
	}
}

func TestContentStreamDelimiterRobustness(t *testing.T) {
	// Content stream with stray/unexpected delimiters: ')' outside string, '}', '{'
	strayContent := "BT ) } { /F1 12 Tf 1 0 0 1 100 700 Tm (Safe Text) Tj ET\n"
	pdfBytes := buildSimplePDF(strayContent)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	doc, err := pdfextract.ExtractBytes(ctx, pdfBytes, pdfextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes failed on stray delimiters: %v", err)
	}

	if !strings.Contains(doc.Markdown, "Safe Text") {
		t.Errorf("Expected 'Safe Text' in markdown, got:\n%s", doc.Markdown)
	}
}

func TestUnicodePunctuationAndFullwidth(t *testing.T) {
	// Test full-width ASCII, soft-hyphens, and non-breaking spaces
	raw := "Hello\u00A0World\u00AD! \uFF08Fullwidth\uFF09"
	cleaned := pdfextract.CleanText(raw)
	expected := "Hello World! (Fullwidth)"
	if cleaned != expected {
		t.Errorf("Expected %q, got %q", expected, cleaned)
	}
}
