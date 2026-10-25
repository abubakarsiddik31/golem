package docextract_test

import (
	"context"
	"testing"

	"github.com/abubakarsiddik31/golem/docextract"
)

// BenchmarkExtractDocx measures the whole Office path for a Word
// document: zip inflation, content-type and relationship resolution,
// document.xml parsing, table extraction, and Markdown rendering.
func BenchmarkExtractDocx(b *testing.B) {
	data := buildSampleDocx(b)
	ctx := context.Background()

	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		doc, err := docextract.ExtractBytes(ctx, data, "sample.docx", docextract.Options{})
		if err != nil {
			b.Fatal(err)
		}
		if doc.Content == "" {
			b.Fatal("extraction produced no content")
		}
	}
}

// BenchmarkExtractXlsx measures the spreadsheet path: shared-string
// resolution, cell-reference decoding, and per-sheet Markdown table
// rendering.
func BenchmarkExtractXlsx(b *testing.B) {
	data := buildSampleXlsx(b)
	ctx := context.Background()

	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		doc, err := docextract.ExtractBytes(ctx, data, "sample.xlsx", docextract.Options{})
		if err != nil {
			b.Fatal(err)
		}
		if doc.Content == "" {
			b.Fatal("extraction produced no content")
		}
	}
}

// BenchmarkExtractPptx measures the slide path, where each slide's shape
// tree is walked and its text collected in reading order.
func BenchmarkExtractPptx(b *testing.B) {
	data := buildSamplePptx(b)
	ctx := context.Background()

	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		doc, err := docextract.ExtractBytes(ctx, data, "sample.pptx", docextract.Options{})
		if err != nil {
			b.Fatal(err)
		}
		if doc.Content == "" {
			b.Fatal("extraction produced no content")
		}
	}
}

// BenchmarkExtractDocxOutline isolates outline-only extraction, which
// walks the structure without building the body text. The difference
// against BenchmarkExtractDocx is what full rendering costs.
func BenchmarkExtractDocxOutline(b *testing.B) {
	data := buildSampleDocx(b)
	ctx := context.Background()

	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		doc, err := docextract.ExtractBytes(ctx, data, "sample.docx", docextract.Options{Outline: true})
		if err != nil {
			b.Fatal(err)
		}
		if len(doc.Outline) == 0 {
			b.Fatal("outline extraction found no headings")
		}
	}
}
