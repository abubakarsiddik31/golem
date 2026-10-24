package docextract_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/abubakarsiddik31/golem/docextract"
)

// docxWithDeclaredSize builds a valid docx-shaped archive and rewrites
// the central directory's uncompressed size for word/document.xml. The
// compressed bytes stay tiny, so this exercises the declared-size check
// without allocating a real zip bomb.
func docxWithDeclaredSize(t *testing.T, declared uint32) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	if _, err := entry.Write([]byte("<w:document/>")); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	data := buffer.Bytes()
	central := bytes.Index(data, []byte("PK\x01\x02"))
	if central < 0 {
		t.Fatal("central directory header not found")
	}
	// The uncompressed size field is at offset 24 of the central header.
	binary.LittleEndian.PutUint32(data[central+24:central+28], declared)
	return data
}

func TestExtractBytesRejectsDeclaredOversizedEntry(t *testing.T) {
	data := docxWithDeclaredSize(t, uint32(docextract.DefaultMaxEntryBytes+1))
	_, err := docextract.ExtractBytes(context.Background(), data, "bomb.docx", docextract.Options{})
	var limitErr *docextract.DecompressionLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("ExtractBytes error = %v, want *docextract.DecompressionLimitError", err)
	}
	if limitErr.Entry != "word/document.xml" || limitErr.Total {
		t.Errorf("error = %+v, want a per-entry limit at word/document.xml", limitErr)
	}
	if limitErr.Limit != docextract.DefaultMaxEntryBytes {
		t.Errorf("Limit = %d, want %d", limitErr.Limit, docextract.DefaultMaxEntryBytes)
	}
}

func TestExtractBytesAcceptsArchiveWithinBudget(t *testing.T) {
	data := docxWithDeclaredSize(t, 13)
	doc, err := docextract.ExtractBytes(context.Background(), data, "small.docx", docextract.Options{})
	if err != nil {
		t.Fatalf("ExtractBytes error = %v", err)
	}
	if doc.Format != docextract.FormatDocx {
		t.Errorf("Format = %v, want %v", doc.Format, docextract.FormatDocx)
	}
}
