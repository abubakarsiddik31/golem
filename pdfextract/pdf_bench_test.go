package pdfextract_test

import (
	"context"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/pdfextract"
)

// representativePage is one page with a heading, two text columns, and a
// ruled table: the layout work a real report exercises. It is the same
// content the extract benchmarks share so a change in one is comparable
// with the other.
func representativePage() string {
	var content strings.Builder
	content.WriteString("BT /F1 20 Tf 50 720 Td (Annual Engineering Report) Tj ET\n")
	// Left column.
	content.WriteString("BT /F1 12 Tf 50 680 Td (Left column paragraph with detailed architecture notes.) Tj ET\n")
	content.WriteString("BT /F1 12 Tf 50 650 Td (Second line of the left column description.) Tj ET\n")
	// Right column.
	content.WriteString("BT /F1 12 Tf 350 680 Td (Right column begins here with performance metrics.) Tj ET\n")
	content.WriteString("BT /F1 12 Tf 350 650 Td (Second line of the right column evaluation.) Tj ET\n")
	// Ruled table.
	content.WriteString("50 550 m 300 550 l S\n50 525 m 300 525 l S\n50 500 m 300 500 l S\n")
	content.WriteString("50 500 m 50 550 l S\n175 500 m 175 550 l S\n300 500 m 300 550 l S\n")
	content.WriteString("BT /F1 10 Tf 60 535 Td (Metric) Tj ET\n")
	content.WriteString("BT /F1 10 Tf 185 535 Td (Score) Tj ET\n")
	content.WriteString("BT /F1 10 Tf 60 510 Td (Latency) Tj ET\n")
	content.WriteString("BT /F1 10 Tf 185 510 Td (4.2ms) Tj ET\n")
	return content.String()
}

// BenchmarkExtractSinglePage measures the full pipeline — parse, layout
// analysis, table reconstruction, and Markdown rendering — on one
// representative page. The reported MB/s is input PDF bytes.
func BenchmarkExtractSinglePage(b *testing.B) {
	pdfBytes := buildSimplePDF(representativePage())
	ctx := context.Background()

	b.SetBytes(int64(len(pdfBytes)))
	b.ReportAllocs()
	for b.Loop() {
		doc, err := pdfextract.ExtractBytes(ctx, pdfBytes, pdfextract.Options{})
		if err != nil {
			b.Fatal(err)
		}
		if len(doc.Markdown) == 0 {
			b.Fatal("extraction produced no Markdown")
		}
	}
}

// BenchmarkExtractMultiPage measures a twenty-page document, the shape
// where per-page cost and cumulative allocation are what matter. Divide
// the reported MB/s by page count for per-page throughput.
func BenchmarkExtractMultiPage(b *testing.B) {
	pages := make([]string, 20)
	for i := range pages {
		pages[i] = representativePage()
	}
	pdfBytes := buildMultiPagePDF(pages)
	ctx := context.Background()

	b.SetBytes(int64(len(pdfBytes)))
	b.ReportAllocs()
	for b.Loop() {
		doc, err := pdfextract.ExtractBytes(ctx, pdfBytes, pdfextract.Options{})
		if err != nil {
			b.Fatal(err)
		}
		if doc.NumPages != len(pages) {
			b.Fatalf("NumPages = %d, want %d", doc.NumPages, len(pages))
		}
	}
}

// BenchmarkParsePDF isolates the parser — object graph, stream decoding,
// and page extraction — from layout analysis and Markdown rendering, so
// a regression can be attributed to one half of the pipeline.
func BenchmarkParsePDF(b *testing.B) {
	pdfBytes := buildMultiPagePDF([]string{
		representativePage(), representativePage(), representativePage(),
		representativePage(), representativePage(),
	})

	b.SetBytes(int64(len(pdfBytes)))
	b.ReportAllocs()
	for b.Loop() {
		doc, err := pdfextract.ParsePDF(pdfBytes)
		if err != nil {
			b.Fatal(err)
		}
		if doc.NumPages() == 0 {
			b.Fatal("parsed no pages")
		}
	}
}
