package pdfextract_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/pdfextract"
)

// firstArgValue returns the value following flag in argv, or "" if absent.
func firstArgValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func hasArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func TestIndraOCREngineRecognizesScannedPage(t *testing.T) {
	var gotArgs []string
	var imagePath string

	engine, err := pdfextract.NewIndraOCR(pdfextract.IndraOCRConfig{
		Run: func(_ context.Context, name string, args []string) ([]byte, error) {
			if name != "indra-ocr" {
				t.Errorf("binary = %q, want indra-ocr", name)
			}
			gotArgs = args
			imagePath = args[0]
			return []byte(`{"markdown":"# Scanned Receipt\n\n| Item | Cost |\n| --- | --- |\n| Coffee | $4.50 |","source":"ocr"}`), nil
		},
	})
	if err != nil {
		t.Fatalf("NewIndraOCR failed: %v", err)
	}

	doc, err := pdfextract.ExtractBytes(context.Background(), buildScannedPDF(), pdfextract.Options{
		OCREngine: engine,
	})
	if err != nil {
		t.Fatalf("ExtractBytes failed: %v", err)
	}

	if !strings.Contains(doc.Markdown, "# Scanned Receipt") {
		t.Errorf("Expected Indra markdown heading, got:\n%s", doc.Markdown)
	}
	if !strings.Contains(doc.Markdown, "| Coffee | $4.50 |") {
		t.Errorf("Expected Indra markdown table, got:\n%s", doc.Markdown)
	}

	if got := firstArgValue(gotArgs, "--format"); got != "json" {
		t.Errorf("--format = %q, want json", got)
	}
	if !strings.HasSuffix(imagePath, ".png") {
		t.Errorf("page image path = %q, want a .png temp file", imagePath)
	}
	// The temp image must not outlive the call.
	if _, err := os.Stat(imagePath); !os.IsNotExist(err) {
		t.Errorf("temp image %q was not removed (err = %v)", imagePath, err)
	}
}

func TestIndraOCREnginePassesConfiguredFlags(t *testing.T) {
	var gotArgs []string
	engine, err := pdfextract.NewIndraOCR(pdfextract.IndraOCRConfig{
		ModelDir:      "/models",
		DPI:           300,
		Cores:         4,
		KeepFurniture: true,
		Run: func(_ context.Context, _ string, args []string) ([]byte, error) {
			gotArgs = args
			// Newline-delimited page objects must be joined in order.
			return []byte("{\"markdown\":\"first\",\"source\":\"ocr\"}\n{\"markdown\":\"second\",\"source\":\"ocr\"}\n"), nil
		},
	})
	if err != nil {
		t.Fatalf("NewIndraOCR failed: %v", err)
	}

	res, err := engine.RecognizeMarkdown(context.Background(), []byte("img"), "jpeg", pdfextract.Rect{})
	if err != nil {
		t.Fatalf("RecognizeMarkdown failed: %v", err)
	}
	if res.Markdown != "first\n\nsecond" {
		t.Errorf("Markdown = %q, want %q", res.Markdown, "first\n\nsecond")
	}

	if got := firstArgValue(gotArgs, "--model-dir"); got != "/models" {
		t.Errorf("--model-dir = %q, want /models", got)
	}
	if got := firstArgValue(gotArgs, "--dpi"); got != "300" {
		t.Errorf("--dpi = %q, want 300", got)
	}
	if got := firstArgValue(gotArgs, "--cores"); got != "4" {
		t.Errorf("--cores = %q, want 4", got)
	}
	if !hasArg(gotArgs, "--keep-furniture") {
		t.Errorf("expected --keep-furniture in %v", gotArgs)
	}
	if !strings.HasSuffix(gotArgs[0], ".jpg") {
		t.Errorf("jpeg format should use a .jpg temp path, got %q", gotArgs[0])
	}
}

func TestIndraOCREngineRunErrorPropagation(t *testing.T) {
	sentinel := errors.New("model weights missing")
	engine, err := pdfextract.NewIndraOCR(pdfextract.IndraOCRConfig{
		Run: func(_ context.Context, _ string, _ []string) ([]byte, error) {
			return nil, sentinel
		},
	})
	if err != nil {
		t.Fatalf("NewIndraOCR failed: %v", err)
	}

	_, err = pdfextract.ExtractBytes(context.Background(), buildScannedPDF(), pdfextract.Options{
		OCREngine: engine,
	})
	if err == nil {
		t.Fatalf("Expected error when the OCR subprocess fails")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("Expected wrapped sentinel error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "pdfextract: indra ocr") {
		t.Errorf("Expected pdfextract: indra ocr prefix, got: %v", err)
	}
}

func TestIndraOCREnginePageError(t *testing.T) {
	engine, err := pdfextract.NewIndraOCR(pdfextract.IndraOCRConfig{
		Run: func(_ context.Context, _ string, _ []string) ([]byte, error) {
			return []byte(`{"source":"error","note":"unreadable image"}`), nil
		},
	})
	if err != nil {
		t.Fatalf("NewIndraOCR failed: %v", err)
	}

	_, err = engine.RecognizeMarkdown(context.Background(), []byte("img"), "png", pdfextract.Rect{})
	if err == nil {
		t.Fatalf("Expected error for source=error page")
	}
	if !strings.Contains(err.Error(), "unreadable image") {
		t.Errorf("Expected note in error, got: %v", err)
	}
}

func TestIndraOCREngineMalformedOutput(t *testing.T) {
	engine, err := pdfextract.NewIndraOCR(pdfextract.IndraOCRConfig{
		Run: func(_ context.Context, _ string, _ []string) ([]byte, error) {
			return []byte("not json"), nil
		},
	})
	if err != nil {
		t.Fatalf("NewIndraOCR failed: %v", err)
	}

	if _, err := engine.RecognizeMarkdown(context.Background(), []byte("img"), "png", pdfextract.Rect{}); err == nil {
		t.Fatalf("Expected decode error for malformed CLI output")
	}
}

func TestIndraOCREngineCancellation(t *testing.T) {
	called := false
	engine, err := pdfextract.NewIndraOCR(pdfextract.IndraOCRConfig{
		Run: func(_ context.Context, _ string, _ []string) ([]byte, error) {
			called = true
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("NewIndraOCR failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = engine.RecognizeMarkdown(ctx, []byte("img"), "png", pdfextract.Rect{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled, got: %v", err)
	}
	if called {
		t.Errorf("OCR subprocess must not run on a cancelled context")
	}
}

func TestIndraOCREngineEmptyImage(t *testing.T) {
	called := false
	engine, err := pdfextract.NewIndraOCR(pdfextract.IndraOCRConfig{
		Run: func(_ context.Context, _ string, _ []string) ([]byte, error) {
			called = true
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("NewIndraOCR failed: %v", err)
	}

	res, err := engine.RecognizeMarkdown(context.Background(), nil, "png", pdfextract.Rect{})
	if err != nil {
		t.Fatalf("RecognizeMarkdown failed: %v", err)
	}
	if res.Markdown != "" {
		t.Errorf("Markdown = %q, want empty", res.Markdown)
	}
	if called {
		t.Errorf("OCR subprocess must not run for an empty image")
	}
}

func TestIndraOCREngineRecognizePageSpans(t *testing.T) {
	engine, err := pdfextract.NewIndraOCR(pdfextract.IndraOCRConfig{
		Run: func(_ context.Context, _ string, _ []string) ([]byte, error) {
			return []byte(`{"markdown":"line one\nline two","source":"ocr"}`), nil
		},
	})
	if err != nil {
		t.Fatalf("NewIndraOCR failed: %v", err)
	}

	spans, err := engine.RecognizePage(context.Background(), []byte("img"), "png", pdfextract.Rect{X0: 0, Y0: 0, X1: 612, Y1: 792})
	if err != nil {
		t.Fatalf("RecognizePage failed: %v", err)
	}
	var texts []string
	for _, s := range spans {
		texts = append(texts, s.Text)
	}
	if len(spans) != 2 || texts[0] != "line one" || texts[1] != "line two" {
		t.Errorf("RecognizePage spans = %v, want [line one, line two]", texts)
	}
}

func TestIndraOCRBinaryNotFound(t *testing.T) {
	_, err := pdfextract.NewIndraOCR(pdfextract.IndraOCRConfig{
		BinaryPath: "golem-indra-binary-does-not-exist",
	})
	if err == nil {
		t.Fatalf("Expected error when the indra-ocr binary is absent from PATH")
	}
	if !strings.Contains(err.Error(), "not found in PATH") {
		t.Errorf("Expected a PATH hint, got: %v", err)
	}
}
