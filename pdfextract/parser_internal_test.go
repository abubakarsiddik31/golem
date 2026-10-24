package pdfextract

import (
	"bytes"
	"compress/zlib"
	"encoding/ascii85"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDecompressZlibRejectsOversizedStream(t *testing.T) {
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	chunk := make([]byte, 1<<20)
	for i := 0; i < 65; i++ { // 65 MiB of zeros, over the 64 MiB bound
		if _, err := zw.Write(chunk); err != nil {
			t.Fatalf("zlib write failed: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zlib close failed: %v", err)
	}

	if _, err := decompressZlib(compressed.Bytes()); err == nil {
		t.Fatal("expected an error for a stream that expands past the bound")
	}
}

func TestTruncateUTF8DoesNotSplitRunes(t *testing.T) {
	s := "aé" // 1 + 2 bytes; cutting at 2 lands inside é
	got := truncateUTF8(s, 2)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateUTF8 returned invalid UTF-8: %q", got)
	}
	if got != "a" {
		t.Fatalf("truncateUTF8 = %q, want %q", got, "a")
	}
}

func TestStreamFiltersHandlesArray(t *testing.T) {
	dict := pdfDict{
		"Filter": pdfArray{pdfName("ASCII85Decode"), pdfName("FlateDecode")},
	}
	got := streamFilters(dict)
	want := []string{"ASCII85Decode", "FlateDecode"}
	if len(got) != len(want) {
		t.Fatalf("streamFilters = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("streamFilters = %v, want %v", got, want)
		}
	}
}

func TestDecodeASCIIHex(t *testing.T) {
	got, err := decodeASCIIHex([]byte("48656C6C6F>"))
	if err != nil {
		t.Fatalf("decodeASCIIHex failed: %v", err)
	}
	if string(got) != "Hello" {
		t.Fatalf("decodeASCIIHex = %q, want %q", got, "Hello")
	}
}

func TestDecodeASCII85(t *testing.T) {
	want := []byte("Hello, PDF!")
	encoded := make([]byte, ascii85.MaxEncodedLen(len(want)))
	n := ascii85.Encode(encoded, want)
	got, err := decodeASCII85(append([]byte("<~"), append(encoded[:n], []byte("~>")...)...))
	if err != nil {
		t.Fatalf("decodeASCII85 failed: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("decodeASCII85 = %q, want %q", got, want)
	}
}

func TestStreamFiltersSingleName(t *testing.T) {
	got := streamFilters(pdfDict{"Filter": pdfName("FlateDecode")})
	if len(got) != 1 || got[0] != "FlateDecode" {
		t.Fatalf("streamFilters = %v, want [FlateDecode]", got)
	}
}

func TestDecodeStreamRejectsUnknownFilter(t *testing.T) {
	doc := &PDFDoc{}
	_, err := doc.decodeStream(pdfStream{Dict: pdfDict{"Filter": pdfName("JBIG2Decode")}})
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("err = %v, want an unsupported-filter error", err)
	}
}
