package docextract_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/docextract"
	"github.com/abubakarsiddik31/golem/model"
)

func TestDocxExtraction(t *testing.T) {
	docxData := buildSampleDocx(t)

	doc, err := docextract.ExtractBytes(context.Background(), docxData, "report.docx", docextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes docx: %v", err)
	}

	if doc.Format != docextract.FormatDocx {
		t.Fatalf("doc.Format = %v, want %v", doc.Format, docextract.FormatDocx)
	}

	// Check headings
	if !strings.Contains(doc.Content, "# Quarterly Report") {
		t.Errorf("content missing H1 '# Quarterly Report', got:\n%s", doc.Content)
	}
	if !strings.Contains(doc.Content, "## Financial Summary") {
		t.Errorf("content missing H2 '## Financial Summary', got:\n%s", doc.Content)
	}

	// Check formatting: bold, italic, link
	if !strings.Contains(doc.Content, "**strong growth**") {
		t.Errorf("content missing bold text, got:\n%s", doc.Content)
	}
	if !strings.Contains(doc.Content, "*record revenue*") {
		t.Errorf("content missing italic text, got:\n%s", doc.Content)
	}
	if !strings.Contains(doc.Content, "[SEC Filing](https://example.com/sec)") {
		t.Errorf("content missing hyperlink, got:\n%s", doc.Content)
	}

	// Check lists
	if !strings.Contains(doc.Content, "- Revenue increased by 25%") {
		t.Errorf("content missing bullet list item, got:\n%s", doc.Content)
	}

	// Check table
	if !strings.Contains(doc.Content, "| Metric | Q1 | Q2 |") {
		t.Errorf("content missing table header, got:\n%s", doc.Content)
	}
	if !strings.Contains(doc.Content, "| Revenue | $10M | $12.5M |") {
		t.Errorf("content missing table row, got:\n%s", doc.Content)
	}

	// Check outline items
	if len(doc.Outline) < 2 {
		t.Fatalf("expected at least 2 outline items, got %d", len(doc.Outline))
	}
	if doc.Outline[0].Title != "Quarterly Report" || doc.Outline[0].Level != 1 {
		t.Errorf("outline[0] = %+v, want Quarterly Report level 1", doc.Outline[0])
	}
	if doc.Outline[1].Title != "Financial Summary" || doc.Outline[1].Level != 2 {
		t.Errorf("outline[1] = %+v, want Financial Summary level 2", doc.Outline[1])
	}

	// Check RenderOutline
	outlineStr := doc.RenderOutline()
	if !strings.Contains(outlineStr, "- Quarterly Report") || !strings.Contains(outlineStr, "  - Financial Summary") {
		t.Errorf("RenderOutline() unexpected:\n%s", outlineStr)
	}
}

func TestDocxNestedTable(t *testing.T) {
	docxData := buildNestedTableDocx(t)

	doc, err := docextract.ExtractBytes(context.Background(), docxData, "nested.docx", docextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes docx nested table: %v", err)
	}

	if !strings.Contains(doc.Content, "| Outer Col 1 | Outer Col 2 |") {
		t.Errorf("content missing outer table:\n%s", doc.Content)
	}
	if !strings.Contains(doc.Content, "Inner A") || !strings.Contains(doc.Content, "Inner B") {
		t.Errorf("content missing inner table cells:\n%s", doc.Content)
	}
}

func TestDocxQueryFiltering(t *testing.T) {
	docxData := buildSampleDocx(t)

	doc, err := docextract.ExtractBytes(context.Background(), docxData, "report.docx", docextract.Options{
		Query: "Financial Summary",
	})
	if err != nil {
		t.Fatalf("ExtractBytes docx with query: %v", err)
	}

	if !strings.Contains(doc.Content, "## Financial Summary") {
		t.Errorf("content missing queried section, got:\n%s", doc.Content)
	}
	if strings.Contains(doc.Content, "# Quarterly Report") {
		t.Errorf("content should not contain preceding section, got:\n%s", doc.Content)
	}
}

func TestXlsxExtraction(t *testing.T) {
	xlsxData := buildSampleXlsx(t)

	doc, err := docextract.ExtractBytes(context.Background(), xlsxData, "data.xlsx", docextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes xlsx: %v", err)
	}

	if doc.Format != docextract.FormatXlsx {
		t.Fatalf("doc.Format = %v, want %v", doc.Format, docextract.FormatXlsx)
	}

	// Check sheets
	if !strings.Contains(doc.Content, "## Sheet: Q1 Sales") {
		t.Errorf("content missing Sheet: Q1 Sales, got:\n%s", doc.Content)
	}
	if !strings.Contains(doc.Content, "## Sheet: Summary") {
		t.Errorf("content missing Sheet: Summary, got:\n%s", doc.Content)
	}

	// Check table contents in Q1 Sales
	if !strings.Contains(doc.Content, "| Product | Units | Revenue |") {
		t.Errorf("content missing table header, got:\n%s", doc.Content)
	}
	if !strings.Contains(doc.Content, "| Widget A | 100 | 2500 |") {
		t.Errorf("content missing row 1, got:\n%s", doc.Content)
	}
	if !strings.Contains(doc.Content, "| Widget B | 200 | 5000 |") {
		t.Errorf("content missing row 2, got:\n%s", doc.Content)
	}

	// Check outline
	if len(doc.Outline) != 2 {
		t.Fatalf("expected 2 sheets in outline, got %d", len(doc.Outline))
	}
	if doc.Outline[0].Title != "Q1 Sales" {
		t.Errorf("outline[0].Title = %q, want 'Q1 Sales'", doc.Outline[0].Title)
	}
}

func TestXlsxSheetFilter(t *testing.T) {
	xlsxData := buildSampleXlsx(t)

	// Filter by sheet name via query
	doc, err := docextract.ExtractBytes(context.Background(), xlsxData, "data.xlsx", docextract.Options{
		Query: "Summary",
	})
	if err != nil {
		t.Fatalf("ExtractBytes xlsx filter: %v", err)
	}

	if !strings.Contains(doc.Content, "## Sheet: Summary") {
		t.Errorf("content missing filtered sheet 'Summary', got:\n%s", doc.Content)
	}
	if strings.Contains(doc.Content, "## Sheet: Q1 Sales") {
		t.Errorf("content should not contain unfiltered sheet 'Q1 Sales', got:\n%s", doc.Content)
	}
}

func TestPptxExtraction(t *testing.T) {
	pptxData := buildSamplePptx(t)

	doc, err := docextract.ExtractBytes(context.Background(), pptxData, "slides.pptx", docextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes pptx: %v", err)
	}

	if doc.Format != docextract.FormatPptx {
		t.Fatalf("doc.Format = %v, want %v", doc.Format, docextract.FormatPptx)
	}

	// Check slide 1
	if !strings.Contains(doc.Content, "## Slide 1: Welcome to Golem") {
		t.Errorf("content missing Slide 1, got:\n%s", doc.Content)
	}
	if !strings.Contains(doc.Content, "- AI Agent Framework") {
		t.Errorf("content missing Slide 1 bullets, got:\n%s", doc.Content)
	}
	if !strings.Contains(doc.Content, "> **Speaker Notes:**") || !strings.Contains(doc.Content, "Introduce the team and agenda") {
		t.Errorf("content missing speaker notes, got:\n%s", doc.Content)
	}

	// Check slide 2
	if !strings.Contains(doc.Content, "## Slide 2: Architecture") {
		t.Errorf("content missing Slide 2, got:\n%s", doc.Content)
	}

	// Check outline
	if len(doc.Outline) != 2 {
		t.Fatalf("expected 2 slides in outline, got %d", len(doc.Outline))
	}
	if doc.Outline[0].Title != "Welcome to Golem" {
		t.Errorf("outline[0].Title = %q, want 'Welcome to Golem'", doc.Outline[0].Title)
	}
}

func TestMarkdownExtraction(t *testing.T) {
	mdContent := `---
author: Alice
date: 2026-09-19
---

# Project Overview

This is a modern agent framework.

## Installation

Run go get github.com/abubakarsiddik31/golem

## Configuration

Set up your agent options here.
`

	doc, err := docextract.ExtractBytes(context.Background(), []byte(mdContent), "spec.md", docextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes markdown: %v", err)
	}

	if doc.Format != docextract.FormatMarkdown {
		t.Fatalf("doc.Format = %v, want %v", doc.Format, docextract.FormatMarkdown)
	}

	// Metadata
	if doc.Metadata["author"] != "Alice" {
		t.Errorf("metadata author = %q, want Alice", doc.Metadata["author"])
	}

	// Outline
	if len(doc.Outline) != 3 {
		t.Fatalf("expected 3 outline items, got %d", len(doc.Outline))
	}
	if doc.Outline[0].Title != "Project Overview" || doc.Outline[0].Level != 1 {
		t.Errorf("outline[0] = %+v", doc.Outline[0])
	}
	if doc.Outline[1].Title != "Installation" || doc.Outline[1].Level != 2 {
		t.Errorf("outline[1] = %+v", doc.Outline[1])
	}

	// Section query
	docSec, err := docextract.ExtractBytes(context.Background(), []byte(mdContent), "spec.md", docextract.Options{
		Query: "Installation",
	})
	if err != nil {
		t.Fatalf("ExtractBytes section query: %v", err)
	}
	if !strings.Contains(docSec.Content, "## Installation") {
		t.Errorf("missing ## Installation in section query, got:\n%s", docSec.Content)
	}
	if strings.Contains(docSec.Content, "# Project Overview") || strings.Contains(docSec.Content, "## Configuration") {
		t.Errorf("unexpected content outside queried section, got:\n%s", docSec.Content)
	}
}

func TestCSVExtraction(t *testing.T) {
	csvContent := `Year,Product,Sales
2024,Widget,100
2025,Gadget,200
2026,Widget,350
`
	doc, err := docextract.ExtractBytes(context.Background(), []byte(csvContent), "data.csv", docextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes csv: %v", err)
	}

	if doc.Format != docextract.FormatCSV {
		t.Fatalf("doc.Format = %v, want %v", doc.Format, docextract.FormatCSV)
	}

	if !strings.Contains(doc.Content, "| Year | Product | Sales |") {
		t.Errorf("content missing table header, got:\n%s", doc.Content)
	}
	if !strings.Contains(doc.Content, "| 2026 | Widget | 350 |") {
		t.Errorf("content missing row, got:\n%s", doc.Content)
	}

	// Query filter on CSV
	docQuery, err := docextract.ExtractBytes(context.Background(), []byte(csvContent), "data.csv", docextract.Options{
		Query: "Gadget",
	})
	if err != nil {
		t.Fatalf("ExtractBytes csv query: %v", err)
	}
	if !strings.Contains(docQuery.Content, "| 2025 | Gadget | 200 |") {
		t.Errorf("missing matching row in CSV query, got:\n%s", docQuery.Content)
	}
	if strings.Contains(docQuery.Content, "| 2024 | Widget | 100 |") {
		t.Errorf("non-matching row should be filtered out, got:\n%s", docQuery.Content)
	}
}

func TestPDFRouting(t *testing.T) {
	pdfBytes := buildSimplePDF("PDF document extraction test.")

	doc, err := docextract.ExtractBytes(context.Background(), pdfBytes, "sample.pdf", docextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes pdf: %v", err)
	}

	if doc.Format != docextract.FormatPDF {
		t.Fatalf("doc.Format = %v, want %v", doc.Format, docextract.FormatPDF)
	}
	if !strings.Contains(doc.Content, "PDF document extraction test.") {
		t.Errorf("extracted PDF content missing expected text, got:\n%s", doc.Content)
	}
}

func TestToolExecutionAndSecurityConfinement(t *testing.T) {
	tempDir := t.TempDir()

	// Write a sample docx in root
	docxData := buildSampleDocx(t)
	if err := os.WriteFile(filepath.Join(tempDir, "doc.docx"), docxData, 0o644); err != nil {
		t.Fatal(err)
	}

	// Write a secret file outside root
	secretDir := t.TempDir()
	secretPath := filepath.Join(secretDir, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("super secret data"), 0o644); err != nil {
		t.Fatal(err)
	}

	toolInstance, err := docextract.New[struct{}](docextract.Config{
		Root: tempDir,
	})
	if err != nil {
		t.Fatalf("docextract.New: %v", err)
	}

	if toolInstance.Name != docextract.ToolName {
		t.Fatalf("tool name = %q, want %q", toolInstance.Name, docextract.ToolName)
	}

	// 1. Successful extraction
	res, err := toolInstance.Exec(context.Background(), struct{}{}, json.RawMessage(`{"path": "doc.docx"}`))
	if err != nil {
		t.Fatalf("tool execution error: %v", err)
	}
	if !strings.Contains(res.Text, "# Quarterly Report") {
		t.Errorf("tool output missing expected text, got:\n%s", res.Text)
	}

	// 2. Outline mode
	resOutline, err := toolInstance.Exec(context.Background(), struct{}{}, json.RawMessage(`{"path": "doc.docx", "outline": true}`))
	if err != nil {
		t.Fatalf("tool outline error: %v", err)
	}
	if !strings.Contains(resOutline.Text, "# Document Outline") || !strings.Contains(resOutline.Text, "Quarterly Report") {
		t.Errorf("outline output unexpected:\n%s", resOutline.Text)
	}

	// 3. Traversal attempt ..
	_, err = toolInstance.Exec(context.Background(), struct{}{}, json.RawMessage(`{"path": "../secret.txt"}`))
	if err == nil {
		t.Fatal("expected traversal error, got nil")
	}
	var retry *model.ModelRetry
	if !errors.As(err, &retry) {
		t.Fatalf("expected *model.ModelRetry for traversal, got %T: %v", err, err)
	}

	// 4. Absolute path
	_, err = toolInstance.Exec(context.Background(), struct{}{}, json.RawMessage(fmt.Sprintf(`{"path": %q}`, secretPath)))
	if err == nil {
		t.Fatal("expected error for absolute path, got nil")
	}
	if !errors.As(err, &retry) {
		t.Fatalf("expected *model.ModelRetry for absolute path, got %T: %v", err, err)
	}

	// 5. Escaping symlink
	symlinkPath := filepath.Join(tempDir, "link_outside.txt")
	if err := os.Symlink(secretPath, symlinkPath); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	_, err = toolInstance.Exec(context.Background(), struct{}{}, json.RawMessage(`{"path": "link_outside.txt"}`))
	if err == nil {
		t.Fatal("expected escaping symlink error, got nil")
	}
	if !errors.As(err, &retry) {
		t.Fatalf("expected *model.ModelRetry for escaping symlink, got %T: %v", err, err)
	}

	// 6. Nonexistent file
	_, err = toolInstance.Exec(context.Background(), struct{}{}, json.RawMessage(`{"path": "notfound.docx"}`))
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
	if !errors.As(err, &retry) {
		t.Fatalf("expected *model.ModelRetry for missing file, got %T: %v", err, err)
	}

	// 7. Context cancellation
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = toolInstance.Exec(cancelCtx, struct{}{}, json.RawMessage(`{"path": "doc.docx"}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestTSVAndTextExtraction(t *testing.T) {
	// TSV
	tsvData := "Col1\tCol2\nVal1\tVal2\n"
	docTSV, err := docextract.ExtractBytes(context.Background(), []byte(tsvData), "data.tsv", docextract.Options{})
	if err != nil {
		t.Fatalf("TSV extraction error: %v", err)
	}
	if docTSV.Format != docextract.FormatTSV {
		t.Errorf("docTSV.Format = %v, want %v", docTSV.Format, docextract.FormatTSV)
	}
	if !strings.Contains(docTSV.Content, "| Col1 | Col2 |") {
		t.Errorf("missing TSV table header: %s", docTSV.Content)
	}

	// Text (.txt, .json)
	txtData := "Hello from plain text file."
	docTxt, err := docextract.ExtractBytes(context.Background(), []byte(txtData), "notes.txt", docextract.Options{})
	if err != nil {
		t.Fatalf("Text extraction error: %v", err)
	}
	if docTxt.Format != docextract.FormatText {
		t.Errorf("docTxt.Format = %v, want %v", docTxt.Format, docextract.FormatText)
	}
	if !strings.Contains(docTxt.Content, "Hello from plain text file.") {
		t.Errorf("unexpected content: %s", docTxt.Content)
	}
}

func TestMaxBytesTruncation(t *testing.T) {
	tempDir := t.TempDir()
	longText := strings.Repeat("A long line of text for truncation testing.\n", 100)
	filePath := filepath.Join(tempDir, "long.txt")
	if err := os.WriteFile(filePath, []byte(longText), 0o644); err != nil {
		t.Fatal(err)
	}

	toolInstance := docextract.MustNew[struct{}](docextract.Config{
		Root:     tempDir,
		MaxBytes: 50,
	})

	res, err := toolInstance.Exec(context.Background(), struct{}{}, json.RawMessage(`{"path": "long.txt"}`))
	if err != nil {
		t.Fatalf("tool execution error: %v", err)
	}
	if !strings.Contains(res.Text, "[docextract: document truncated at 50 bytes]") {
		t.Errorf("expected truncation notice, got:\n%s", res.Text)
	}
}

func TestConfigValidation(t *testing.T) {
	// Empty root
	_, err := docextract.New[struct{}](docextract.Config{Root: ""})
	if err == nil {
		t.Fatal("expected error for empty root")
	}

	// Negative MaxBytes
	_, err = docextract.New[struct{}](docextract.Config{Root: t.TempDir(), MaxBytes: -1})
	if err == nil {
		t.Fatal("expected error for negative MaxBytes")
	}

	// Non-existent directory
	_, err = docextract.New[struct{}](docextract.Config{Root: "/non/existent/dir/path"})
	if err == nil {
		t.Fatal("expected error for non-existent root")
	}

	// MustNew panics on error
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected MustNew to panic on invalid config")
		}
	}()
	docextract.MustNew[struct{}](docextract.Config{Root: ""})
}

// Helpers to build in-memory sample Office files

func buildSampleDocx(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// [Content_Types].xml
	ct, err := zw.Create("[Content_Types].xml")
	if err != nil {
		t.Fatal(err)
	}
	ct.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`))

	// word/_rels/document.xml.rels
	rels, err := zw.Create("word/_rels/document.xml.rels")
	if err != nil {
		t.Fatal(err)
	}
	rels.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="https://example.com/sec" TargetMode="External"/>
</Relationships>`))

	// word/document.xml
	docXml, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	docXml.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <w:body>
    <!-- Heading 1 -->
    <w:p>
      <w:pPr><w:pStyle w:val="Heading1"/></w:pPr>
      <w:r><w:t>Quarterly Report</w:t></w:r>
    </w:p>
    <!-- Paragraph with bold and italic -->
    <w:p>
      <w:r><w:t>In this quarter we saw </w:t></w:r>
      <w:r><w:rPr><w:b/></w:rPr><w:t>strong growth</w:t></w:r>
      <w:r><w:t> and achieved </w:t></w:r>
      <w:r><w:rPr><w:i/></w:rPr><w:t>record revenue</w:t></w:r>
      <w:r><w:t>.</w:t></w:r>
    </w:p>
    <!-- List item -->
    <w:p>
      <w:pPr><w:numPr><w:ilvl w:val="0"/></w:numPr></w:pPr>
      <w:r><w:t>Revenue increased by 25%</w:t></w:r>
    </w:p>
    <!-- Hyperlink -->
    <w:p>
      <w:r><w:t>See the full report here: </w:t></w:r>
      <w:hyperlink r:id="rId1">
        <w:r><w:t>SEC Filing</w:t></w:r>
      </w:hyperlink>
    </w:p>
    <!-- Heading 2 -->
    <w:p>
      <w:pPr><w:pStyle w:val="Heading2"/></w:pPr>
      <w:r><w:t>Financial Summary</w:t></w:r>
    </w:p>
    <!-- Table -->
    <w:tbl>
      <w:tr>
        <w:tc><w:p><w:r><w:t>Metric</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>Q1</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>Q2</w:t></w:r></w:p></w:tc>
      </w:tr>
      <w:tr>
        <w:tc><w:p><w:r><w:t>Revenue</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>$10M</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>$12.5M</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>
  </w:body>
</w:document>`))

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildSampleXlsx(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// xl/workbook.xml
	wb, err := zw.Create("xl/workbook.xml")
	if err != nil {
		t.Fatal(err)
	}
	wb.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <sheets>
    <sheet name="Q1 Sales" sheetId="1" r:id="rId1"/>
    <sheet name="Summary" sheetId="2" r:id="rId2"/>
  </sheets>
</workbook>`))

	// xl/_rels/workbook.xml.rels
	rels, err := zw.Create("xl/_rels/workbook.xml.rels")
	if err != nil {
		t.Fatal(err)
	}
	rels.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet2.xml"/>
</Relationships>`))

	// xl/sharedStrings.xml
	sst, err := zw.Create("xl/sharedStrings.xml")
	if err != nil {
		t.Fatal(err)
	}
	sst.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="5" uniqueCount="5">
  <si><t>Product</t></si>
  <si><t>Units</t></si>
  <si><t>Revenue</t></si>
  <si><t>Widget A</t></si>
  <si><t>Widget B</t></si>
</sst>`))

	// xl/worksheets/sheet1.xml
	s1, err := zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	s1.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <sheetData>
    <row r="1">
      <c r="A1" t="s"><v>0</v></c>
      <c r="B1" t="s"><v>1</v></c>
      <c r="C1" t="s"><v>2</v></c>
    </row>
    <row r="2">
      <c r="A2" t="s"><v>3</v></c>
      <c r="B2"><v>100</v></c>
      <c r="C2"><v>2500</v></c>
    </row>
    <row r="3">
      <c r="A3" t="s"><v>4</v></c>
      <c r="B3"><v>200</v></c>
      <c r="C3"><v>5000</v></c>
    </row>
  </sheetData>
</worksheet>`))

	// xl/worksheets/sheet2.xml
	s2, err := zw.Create("xl/worksheets/sheet2.xml")
	if err != nil {
		t.Fatal(err)
	}
	s2.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <sheetData>
    <row r="1">
      <c r="A1" t="inlineStr"><is><t>Status</t></is></c>
      <c r="B1" t="inlineStr"><is><t>Complete</t></is></c>
    </row>
  </sheetData>
</worksheet>`))

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildSamplePptx(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// ppt/presentation.xml
	pres, err := zw.Create("ppt/presentation.xml")
	if err != nil {
		t.Fatal(err)
	}
	pres.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <p:sldIdLst>
    <p:sldId id="256" r:id="rId1"/>
    <p:sldId id="257" r:id="rId2"/>
  </p:sldIdLst>
</p:presentation>`))

	// ppt/_rels/presentation.xml.rels
	rels, err := zw.Create("ppt/_rels/presentation.xml.rels")
	if err != nil {
		t.Fatal(err)
	}
	rels.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide1.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide2.xml"/>
</Relationships>`))

	// ppt/slides/_rels/slide1.xml.rels
	s1Rels, err := zw.Create("ppt/slides/_rels/slide1.xml.rels")
	if err != nil {
		t.Fatal(err)
	}
	s1Rels.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rIdNotes" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesSlide" Target="../notesSlides/notesSlide1.xml"/>
</Relationships>`))

	// ppt/notesSlides/notesSlide1.xml
	notes, err := zw.Create("ppt/notesSlides/notesSlide1.xml")
	if err != nil {
		t.Fatal(err)
	}
	notes.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:notes xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <p:cSld>
    <p:spTree>
      <p:sp>
        <p:txBody>
          <a:p><a:r><a:t>Introduce the team and agenda.</a:t></a:r></a:p>
        </p:txBody>
      </p:sp>
    </p:spTree>
  </p:cSld>
</p:notes>`))

	// ppt/slides/slide1.xml
	s1, err := zw.Create("ppt/slides/slide1.xml")
	if err != nil {
		t.Fatal(err)
	}
	s1.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <p:cSld>
    <p:spTree>
      <!-- Title shape -->
      <p:sp>
        <p:nvSpPr><p:nvPr><p:ph type="title"/></p:nvPr></p:nvSpPr>
        <p:txBody>
          <a:p><a:r><a:t>Welcome to Golem</a:t></a:r></a:p>
        </p:txBody>
      </p:sp>
      <!-- Body shape -->
      <p:sp>
        <p:txBody>
          <a:p><a:r><a:t>AI Agent Framework</a:t></a:r></a:p>
          <a:p><a:r><a:t>High-performance document extraction</a:t></a:r></a:p>
        </p:txBody>
      </p:sp>
    </p:spTree>
  </p:cSld>
</p:sld>`))

	// ppt/slides/slide2.xml
	s2, err := zw.Create("ppt/slides/slide2.xml")
	if err != nil {
		t.Fatal(err)
	}
	s2.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <p:cSld>
    <p:spTree>
      <p:sp>
        <p:nvSpPr><p:nvPr><p:ph type="title"/></p:nvPr></p:nvSpPr>
        <p:txBody>
          <a:p><a:r><a:t>Architecture</a:t></a:r></a:p>
        </p:txBody>
      </p:sp>
    </p:spTree>
  </p:cSld>
</p:sld>`))

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildSimplePDF(text string) []byte {
	var pdf strings.Builder
	pdf.WriteString("%PDF-1.4\n")

	contentStream := fmt.Sprintf("BT /F1 12 Tf 50 700 Td (%s) Tj ET", text)

	objects := []string{
		"<</Type /Catalog /Pages 2 0 R>>",
		"<</Type /Pages /Kids [3 0 R] /Count 1>>",
		fmt.Sprintf("<</Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources <</Font <</F1 5 0 R>>>> >>"),
		fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(contentStream), contentStream),
		"<</Type /Font /Subtype /Type1 /BaseFont /Helvetica>>",
	}

	offsets := make([]int, len(objects))
	for i, obj := range objects {
		offsets[i] = pdf.Len()
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}

	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<</Size %d /Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return []byte(pdf.String())
}

func buildNestedTableDocx(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	ct, _ := zw.Create("[Content_Types].xml")
	ct.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`))

	docXml, _ := zw.Create("word/document.xml")
	docXml.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:tbl>
      <w:tr>
        <w:tc><w:p><w:r><w:t>Outer Col 1</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>Outer Col 2</w:t></w:r></w:p></w:tc>
      </w:tr>
      <w:tr>
        <w:tc>
          <!-- Nested inner table inside first cell of second row -->
          <w:tbl>
            <w:tr>
              <w:tc><w:p><w:r><w:t>Inner A</w:t></w:r></w:p></w:tc>
              <w:tc><w:p><w:r><w:t>Inner B</w:t></w:r></w:p></w:tc>
            </w:tr>
          </w:tbl>
        </w:tc>
        <w:tc><w:p><w:r><w:t>Outer Val 2</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>
  </w:body>
</w:document>`))

	zw.Close()
	return buf.Bytes()
}
