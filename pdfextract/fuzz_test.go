package pdfextract_test

import (
	"context"
	"testing"
	"time"

	"github.com/abubakarsiddik31/golem/pdfextract"
)

// FuzzParsePDF asserts the parser never panics on arbitrary input. Panics are
// recovered at the boundary and returned as errors, so this also checks that
// recovery stays in place.
func FuzzParsePDF(f *testing.F) {
	f.Add(buildSimplePDF("BT /F1 12 Tf 72 700 Td (hello) Tj ET"))
	f.Add(buildScannedPDF())
	f.Add([]byte("%PDF-1.4\n"))
	f.Add([]byte("%PDF-1.7\n1 0 obj\n<</Type/Catalog>>\nendobj\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = pdfextract.ParsePDF(data)
	})
}

// FuzzExtractBytes asserts extraction terminates and never panics on arbitrary
// input. The context bound keeps a pathological file from stalling the fuzzer.
func FuzzExtractBytes(f *testing.F) {
	f.Add(buildSimplePDF("BT /F1 12 Tf 72 700 Td (hello) Tj ET"))
	f.Add(buildScannedPDF())
	f.Add([]byte("%PDF-1.4\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = pdfextract.ExtractBytes(ctx, data, pdfextract.Options{})
	})
}
