package docextract

import (
	"archive/zip"
	"bytes"
	"errors"
	"sort"
	"strings"
	"testing"
)

// zipWithEntries builds an archive with deterministic entry order.
// Iterating the map directly would randomize the archive, and a test
// that asserts which entry crosses a budget would then flake.
func zipWithEntries(t *testing.T, entries map[string]string) *zip.Reader {
	t.Helper()
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := entry.Write([]byte(entries[name])); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	if err != nil {
		t.Fatalf("new zip reader: %v", err)
	}
	return reader
}

func TestNewZipSourceRejectsOversizedEntry(t *testing.T) {
	reader := zipWithEntries(t, map[string]string{"word/document.xml": strings.Repeat("a", 200)})
	_, err := newZipSourceWithLimits(reader, 100, 1<<20)
	var limitErr *DecompressionLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("error = %v, want *DecompressionLimitError", err)
	}
	if limitErr.Entry != "word/document.xml" || limitErr.Limit != 100 || limitErr.Total {
		t.Errorf("error = %+v, want entry word/document.xml, limit 100, total false", limitErr)
	}
}

func TestNewZipSourceRejectsOversizedTotal(t *testing.T) {
	reader := zipWithEntries(t, map[string]string{
		"a.xml": strings.Repeat("a", 100),
		"b.xml": strings.Repeat("b", 100),
	})
	_, err := newZipSourceWithLimits(reader, 1024, 150)
	var limitErr *DecompressionLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("error = %v, want *DecompressionLimitError", err)
	}
	if !limitErr.Total || limitErr.Limit != 150 || limitErr.Entry != "b.xml" {
		t.Errorf("error = %+v, want total limit 150 at b.xml", limitErr)
	}
}

func TestZipSourceOpenCapsEntryAtReadTime(t *testing.T) {
	reader := zipWithEntries(t, map[string]string{"word/document.xml": strings.Repeat("a", 200)})
	source := &zipSource{files: reader.File, maxEntry: 100, maxTotal: 1 << 20, remaining: 1 << 20}
	_, err := source.open(reader.File[0])
	var limitErr *DecompressionLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("open error = %v, want *DecompressionLimitError", err)
	}
	if limitErr.Total || limitErr.Limit != 100 {
		t.Errorf("error = %+v, want a per-entry limit of 100", limitErr)
	}
}

func TestZipSourceOpenCapsTotalAtReadTime(t *testing.T) {
	reader := zipWithEntries(t, map[string]string{"word/document.xml": strings.Repeat("a", 200)})
	source := &zipSource{files: reader.File, maxEntry: 1 << 20, maxTotal: 50, remaining: 50}
	_, err := source.open(reader.File[0])
	var limitErr *DecompressionLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("open error = %v, want *DecompressionLimitError", err)
	}
	if !limitErr.Total || limitErr.Limit != 50 {
		t.Errorf("error = %+v, want a total limit of 50", limitErr)
	}
}

func TestZipSourceOpenWithinBudget(t *testing.T) {
	reader := zipWithEntries(t, map[string]string{"word/document.xml": "hello"})
	source := &zipSource{files: reader.File, maxEntry: 1 << 20, maxTotal: 1 << 20, remaining: 1 << 20}
	rc, err := source.open(reader.File[0])
	if err != nil {
		t.Fatalf("open error = %v", err)
	}
	defer rc.Close()
	var buffer bytes.Buffer
	if _, err := buffer.ReadFrom(rc); err != nil {
		t.Fatalf("read error = %v", err)
	}
	if buffer.String() != "hello" {
		t.Errorf("content = %q, want hello", buffer.String())
	}
	if source.remaining != (1<<20)-5 {
		t.Errorf("remaining = %d, want %d", source.remaining, (1<<20)-5)
	}
}

func TestIsDecompressionLimit(t *testing.T) {
	if !isDecompressionLimit(&DecompressionLimitError{Entry: "a", Limit: 1}) {
		t.Error("isDecompressionLimit(DecompressionLimitError) = false, want true")
	}
	if isDecompressionLimit(errors.New("corrupt entry")) {
		t.Error("isDecompressionLimit(other error) = true, want false")
	}
}

func TestDecompressionLimitErrorText(t *testing.T) {
	entry := &DecompressionLimitError{Entry: "word/document.xml", Limit: 100}
	if want := `docextract: archive entry "word/document.xml" exceeds the decompressed size limit of 100 bytes`; entry.Error() != want {
		t.Errorf("Error() = %q, want %q", entry.Error(), want)
	}
	total := &DecompressionLimitError{Entry: "b.xml", Limit: 150, Total: true}
	if want := `docextract: archive exceeds the total decompressed size limit of 150 bytes at entry "b.xml"`; total.Error() != want {
		t.Errorf("Error() = %q, want %q", total.Error(), want)
	}
}
