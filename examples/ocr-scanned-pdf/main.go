// Command ocr-scanned-pdf demonstrates pdfextract.IndraOCREngine, the local
// subprocess OCR engine for scanned PDFs.
//
// With no arguments it runs a fully offline, scripted walkthrough:
//
//   - a scanned PDF (one page, no text layer, one full-page raster) is built
//     in memory;
//   - the Indra engine is handed a fake subprocess through its Run seam, so
//     the example needs no binary, no model download, and no network;
//   - the OCR'd Markdown is printed.
//
// Pass a PDF path to OCR a real file with the local indra-ocr CLI:
//
//	go run ./examples/ocr-scanned-pdf scan.pdf
//
// The real path requires `pip install indra-ocr`; if the binary is missing the
// example prints install instructions and exits.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/abubakarsiddik31/golem/pdfextract"
)

func main() {
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		runAgainstFile(os.Args[1])
		return
	}
	runOffline()
}

// runAgainstFile OCRs a real PDF with the local CLI.
func runAgainstFile(path string) {
	engine, err := pdfextract.NewIndraOCR(pdfextract.IndraOCRConfig{})
	if err != nil {
		fmt.Println("Indra OCR is not available:", err)
		fmt.Println("Install it with: pip install indra-ocr")
		return
	}

	doc, err := pdfextract.Extract(context.Background(), path, pdfextract.Options{OCREngine: engine})
	if err != nil {
		fmt.Println("Extract error:", err)
		return
	}
	fmt.Printf("OCR'd %d page(s):\n\n%s\n", doc.NumPages, doc.Markdown)
}

// runOffline walks the scanned-PDF path with a faked subprocess.
func runOffline() {
	// In production this seam is left nil: the engine then runs the real
	// `indra-ocr <page.png> --format json` command.
	engine := pdfextract.MustNewIndraOCR(pdfextract.IndraOCRConfig{
		Run: func(_ context.Context, name string, args []string) ([]byte, error) {
			fmt.Printf("→ %s %s\n\n", name, strings.Join(redact(args), " "))
			return []byte(`{"markdown":"# Receipt\n\nACME Corp\n\n| Item | Qty |\n| --- | --- |\n| Widget | 2 |","source":"ocr"}`), nil
		},
	})

	pdfBytes := generateScannedPDF()
	doc, err := pdfextract.ExtractBytes(context.Background(), pdfBytes, pdfextract.Options{
		OCREngine: engine,
	})
	if err != nil {
		fmt.Println("ExtractBytes error:", err)
		return
	}

	if len(doc.Pages) > 0 {
		fmt.Printf("page 1 detected as scanned: %v\n\n", doc.Pages[0].IsScanned)
	}
	fmt.Println("Recovered Markdown:")
	fmt.Println(doc.Markdown)
}

// redact hides the temporary page-image path so output is stable across runs.
func redact(args []string) []string {
	out := make([]string, len(args))
	copy(out, args)
	if len(out) > 0 {
		out[0] = "<page.png>"
	}
	return out
}

// generateScannedPDF builds a one-page PDF whose only content is a full-page
// raster image and therefore has no extractable text layer.
func generateScannedPDF() []byte {
	var objects []string
	objects = append(objects, "<</Type/Catalog/Pages 2 0 R>>")
	objects = append(objects, "<</Type/Pages/Kids[3 0 R]/Count 1>>")
	objects = append(objects, "<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</XObject<</Im1 5 0 R>>>>>>")
	contentStr := "q 612 0 0 792 0 0 cm /Im1 Do Q\n"
	objects = append(objects, fmt.Sprintf("<</Length %d>>\nstream\n%sendstream", len(contentStr), contentStr))
	imgData := []byte{230, 240, 250}
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

	return []byte(sb.String())
}
