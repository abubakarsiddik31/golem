package docextract

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
)

const (
	// DefaultMaxEntryBytes bounds the decompressed size of any single
	// entry in an Office document archive.
	DefaultMaxEntryBytes int64 = 64 << 20 // 64 MiB
	// DefaultMaxUncompressedBytes bounds the total decompressed size of
	// every entry an Office document archive may contain.
	DefaultMaxUncompressedBytes int64 = 256 << 20 // 256 MiB
)

// DecompressionLimitError reports an Office document archive whose
// decompressed contents exceed the extraction budget. The archive is
// rejected before its XML is parsed, so a zip bomb cannot stream
// unbounded data through the decoder.
type DecompressionLimitError struct {
	// Entry is the archive entry that crossed the limit.
	Entry string
	// Limit is the byte budget that was exceeded: DefaultMaxEntryBytes
	// for a single entry, DefaultMaxUncompressedBytes for the archive
	// total.
	Limit int64
	// Total reports that the archive-wide budget was exceeded rather
	// than one entry's budget.
	Total bool
}

func (e *DecompressionLimitError) Error() string {
	if e.Total {
		return fmt.Sprintf("docextract: archive exceeds the total decompressed size limit of %d bytes at entry %q", e.Limit, e.Entry)
	}
	return fmt.Sprintf("docextract: archive entry %q exceeds the decompressed size limit of %d bytes", e.Entry, e.Limit)
}

// zipSource reads Office archive entries under a per-entry and an
// archive-wide decompression budget. The declared sizes in the central
// directory are checked first; every actual read is capped as well, so a
// forged header cannot stream past the budget.
type zipSource struct {
	files     []*zip.File
	maxEntry  int64
	maxTotal  int64
	remaining int64
}

func newZipSource(r *zip.Reader) (*zipSource, error) {
	return newZipSourceWithLimits(r, DefaultMaxEntryBytes, DefaultMaxUncompressedBytes)
}

// openOfficeArchive parses data as an Office archive and applies the
// decompression budget before any entry is read.
func openOfficeArchive(data []byte, format string) (*zipSource, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("docextract: parse %s zip: %w", format, err)
	}
	source, err := newZipSource(r)
	if err != nil {
		return nil, fmt.Errorf("docextract: %s archive: %w", format, err)
	}
	return source, nil
}

func newZipSourceWithLimits(r *zip.Reader, maxEntry, maxTotal int64) (*zipSource, error) {
	source := &zipSource{files: r.File, maxEntry: maxEntry, maxTotal: maxTotal, remaining: maxTotal}
	for _, f := range r.File {
		if f.UncompressedSize64 > uint64(maxEntry) {
			return nil, &DecompressionLimitError{Entry: f.Name, Limit: maxEntry}
		}
		size := int64(f.UncompressedSize64)
		if size > source.remaining {
			return nil, &DecompressionLimitError{Entry: f.Name, Limit: maxTotal, Total: true}
		}
		source.remaining -= size
	}
	return source, nil
}

// open reads one entry fully under the remaining budget. It returns a
// reader over the buffered bytes so callers keep their existing
// io.ReadCloser contract.
func (s *zipSource) open(f *zip.File) (io.ReadCloser, error) {
	limit := s.maxEntry
	total := false
	if s.remaining < limit {
		limit = s.remaining
		total = true
	}
	if limit < 0 {
		limit = 0
	}

	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		if total {
			return nil, &DecompressionLimitError{Entry: f.Name, Limit: s.maxTotal, Total: true}
		}
		return nil, &DecompressionLimitError{Entry: f.Name, Limit: s.maxEntry}
	}
	s.remaining -= int64(len(data))
	return io.NopCloser(bytes.NewReader(data)), nil
}

// isDecompressionLimit reports whether err is the archive budget being
// exceeded, as opposed to a corrupt entry the extractor can skip.
func isDecompressionLimit(err error) bool {
	var limitErr *DecompressionLimitError
	return errors.As(err, &limitErr)
}
