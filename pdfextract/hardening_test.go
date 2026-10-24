package pdfextract_test

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/pdfextract"
)

// buildPDFObjects assembles a PDF from raw object bodies (1-indexed by slice
// position) and a caller-supplied trailer dictionary body.
func buildPDFObjects(objects []string, trailer string) []byte {
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
	fmt.Fprintf(&sb, "trailer\n<</Size %d/Root 1 0 R%s>>\nstartxref\n%d\n%%%%EOF\n",
		len(objects)+1, trailer, xrefPos)
	return []byte(sb.String())
}

func buildPDF(objects []string) []byte {
	return buildPDFObjects(objects, "")
}

// TestIsScannedIndependentOfOCRDisable protects the documented contract: a
// scan we chose not to OCR still reports IsScanned true.
func TestIsScannedIndependentOfOCRDisable(t *testing.T) {
	doc, err := pdfextract.ExtractBytes(context.Background(), buildScannedPDF(), pdfextract.Options{
		DisableOCR: true,
	})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}
	if !doc.Pages[0].IsScanned {
		t.Error("IsScanned = false for a scanned page with DisableOCR set")
	}
	if got := doc.Pages[0].Source; got != pdfextract.PageSourceEmpty {
		t.Errorf("Source = %q, want %q", got, pdfextract.PageSourceEmpty)
	}
}

// TestNoopOCRReportsSentinel proves a missing engine is an error, not text.
func TestNoopOCRReportsSentinel(t *testing.T) {
	spans, err := pdfextract.NoopOCR{}.RecognizePage(context.Background(), []byte("x"), "png", pdfextract.Rect{})
	if !errors.Is(err, pdfextract.ErrNoOCREngine) {
		t.Fatalf("err = %v, want ErrNoOCREngine", err)
	}
	if len(spans) != 0 {
		t.Fatalf("spans = %d, want 0 (no fabricated text)", len(spans))
	}
}

func TestDefaultOCREngineReportsUnavailable(t *testing.T) {
	_, err := pdfextract.DefaultOCREngine().RecognizePage(context.Background(), []byte("x"), "png", pdfextract.Rect{})
	if !errors.Is(err, pdfextract.ErrNoOCREngine) {
		t.Fatalf("err = %v, want ErrNoOCREngine", err)
	}
}

// TestScannedPageWithoutEngineHasNoFabricatedText guards against the old
// placeholder span being mistaken for OCR output.
func TestScannedPageWithoutEngineHasNoFabricatedText(t *testing.T) {
	doc, err := pdfextract.ExtractBytes(context.Background(), buildScannedPDF(), pdfextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}
	if got := doc.Pages[0].Source; got != pdfextract.PageSourceEmpty {
		t.Errorf("Source = %q, want %q", got, pdfextract.PageSourceEmpty)
	}
	if strings.Contains(doc.Markdown, "OCR engine not configured") {
		// The marker is acceptable at document level, but it must not arrive as
		// an extracted text span.
		for _, span := range doc.Pages[0].Blocks {
			if strings.Contains(span.Text, "OCR engine not configured") {
				t.Fatalf("placeholder text leaked into layout blocks: %q", span.Text)
			}
		}
	}
}

// TestCommandOCRPassesImagePath proves the command actually receives the page
// image, which the old implementation never did.
func TestCommandOCRPassesImagePath(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "ocr.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntest -s \"$1\" && echo OCR-OK || echo OCR-MISSING\n"), 0755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	engine := &pdfextract.CommandOCR{BinaryPath: script}
	spans, err := engine.RecognizePage(context.Background(), []byte("pixel-bytes"), "png", pdfextract.Rect{X1: 100, Y1: 100})
	if err != nil {
		t.Fatalf("RecognizePage failed: %v", err)
	}
	var got strings.Builder
	for _, s := range spans {
		got.WriteString(s.Text)
	}
	if !strings.Contains(got.String(), "OCR-OK") {
		t.Fatalf("command output = %q, want OCR-OK (image was not passed)", got.String())
	}
}

func TestExtractHonorsMaxBytes(t *testing.T) {
	content := "BT /F1 12 Tf 72 700 Td (" + strings.Repeat("word ", 500) + ") Tj ET"
	doc, err := pdfextract.ExtractBytes(context.Background(), buildSimplePDF(content), pdfextract.Options{
		MaxBytes: 40,
	})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}
	if !strings.Contains(doc.Markdown, "output truncated") {
		t.Fatalf("Markdown was not truncated: %q", doc.Markdown)
	}
	body := strings.SplitN(doc.Markdown, "\n\n[pdfextract:", 2)[0]
	if len(body) > 40 {
		t.Fatalf("truncated body is %d bytes, want <= 40", len(body))
	}
	if !utf8.ValidString(doc.Markdown) {
		t.Fatal("truncated Markdown is not valid UTF-8")
	}
}

// TestFormXObjectTextExtracted covers the most common accuracy gap: page
// content wrapped in a Form XObject.
func TestFormXObjectTextExtracted(t *testing.T) {
	formContent := "BT /F1 12 Tf 72 700 Td (Text inside the form) Tj ET"
	pageContent := "q /Fm1 Do Q"
	objects := []string{
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</XObject<</Fm1 5 0 R>>>>>>",
		fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(pageContent), pageContent),
		fmt.Sprintf("<</Type/XObject/Subtype/Form/BBox[0 0 612 792]/Resources<</Font<</F1 6 0 R>>>>/Length %d>>\nstream\n%s\nendstream", len(formContent), formContent),
		"<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>",
	}

	doc, err := pdfextract.ExtractBytes(context.Background(), buildPDF(objects), pdfextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}
	if !strings.Contains(doc.Markdown, "Text inside the form") {
		t.Fatalf("form XObject text missing from Markdown:\n%s", doc.Markdown)
	}
}

// TestFilterArrayDecodesContent covers /Filter [/FlateDecode], which the old
// single-name check silently skipped.
func TestFilterArrayDecodesContent(t *testing.T) {
	content := "BT /F1 12 Tf 72 700 Td (Compressed via filter array) Tj ET"
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	if _, err := zw.Write([]byte(content)); err != nil {
		t.Fatalf("zlib write failed: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zlib close failed: %v", err)
	}

	objects := []string{
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>",
		fmt.Sprintf("<</Length %d/Filter[/FlateDecode]>>\nstream\n%s\nendstream", compressed.Len(), compressed.String()),
		"<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>",
	}

	doc, err := pdfextract.ExtractBytes(context.Background(), buildPDF(objects), pdfextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}
	if !strings.Contains(doc.Markdown, "Compressed via filter array") {
		t.Fatalf("filter-array content missing from Markdown:\n%s", doc.Markdown)
	}
}

func TestEncryptedPDFReturnsTypedError(t *testing.T) {
	objects := []string{
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]>>",
		"<</Filter/Standard/V 1/R 2/O(deadbeef)/U(deadbeef)/P -1>>",
	}
	pdfBytes := buildPDFObjects(objects, "/Encrypt 4 0 R")

	_, err := pdfextract.ExtractBytes(context.Background(), pdfBytes, pdfextract.Options{})
	if !errors.Is(err, pdfextract.ErrEncrypted) {
		t.Fatalf("err = %v, want ErrEncrypted", err)
	}
}

// TestPageTreeCycleReturnsError proves a /Kids cycle fails instead of
// overflowing the stack.
func TestPageTreeCycleReturnsError(t *testing.T) {
	objects := []string{
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[2 0 R]/Count 1>>",
	}
	_, err := pdfextract.ParsePDF(buildPDF(objects))
	if err == nil {
		t.Fatal("expected an error for a self-referencing page tree")
	}
}

// TestDeepObjectNestingReturnsError proves unbounded recursion is capped.
func TestDeepObjectNestingReturnsError(t *testing.T) {
	deep := strings.Repeat("[", 2000) + strings.Repeat("]", 2000)
	objects := []string{
		"<</Type/Catalog/Pages 2 0 R/Deep " + deep + ">>",
		"<</Type/Pages/Kids[]/Count 0>>",
	}
	_, err := pdfextract.ParsePDF(buildPDF(objects))
	if err == nil {
		t.Fatal("expected an error for deeply nested objects")
	}
}

// TestToolReturnsImageAndScanPartsTogether verifies the two part options are
// independent and de-duplicated.
func TestToolReturnsImageAndScanPartsTogether(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "scanned.pdf"), buildScannedPDF(), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	toolInstance := pdfextract.MustNew[struct{}](pdfextract.Config{
		Root:                   dir,
		ReturnImageParts:       true,
		ReturnScannedPageParts: true,
	})
	res, err := toolInstance.Exec(context.Background(), struct{}{}, json.RawMessage(`{"path": "scanned.pdf"}`))
	if err != nil {
		t.Fatalf("tool Exec failed: %v", err)
	}
	if len(res.Parts) != 1 {
		t.Fatalf("parts = %d, want 1 (the scan should not be duplicated)", len(res.Parts))
	}
	if res.Parts[0].Kind != model.PartImage {
		t.Fatalf("part kind = %v, want image", res.Parts[0].Kind)
	}
}
