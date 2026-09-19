package docextract

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
)

func extractCSV(r io.Reader, comma rune, format Format, opts Options) (*Document, error) {
	reader := csv.NewReader(r)
	reader.Comma = comma
	reader.FieldsPerRecord = -1 // Allow variable columns
	reader.LazyQuotes = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("docextract: parse csv: %w", err)
	}

	if len(records) == 0 {
		return &Document{
			Format:  format,
			Content: "*Empty file*\n",
		}, nil
	}

	maxCols := 0
	for _, row := range records {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}

	totalRows := len(records)

	// Filter rows by query if specified
	var filtered [][]string
	filtered = append(filtered, records[0]) // Always keep header

	qLower := strings.ToLower(strings.TrimSpace(opts.Query))
	if qLower != "" {
		for i := 1; i < len(records); i++ {
			rowMatched := false
			for _, cell := range records[i] {
				if strings.Contains(strings.ToLower(cell), qLower) {
					rowMatched = true
					break
				}
			}
			if rowMatched {
				filtered = append(filtered, records[i])
			}
		}
	} else {
		filtered = records
	}

	maxRows := opts.MaxRowsPerSheet
	if maxRows <= 0 {
		maxRows = 200
	}

	var sb strings.Builder
	displayRows := filtered
	omitted := 0
	if len(filtered) > maxRows {
		displayRows = filtered[:maxRows]
		omitted = len(filtered) - maxRows
	}

	renderGridAsMarkdownTable(&sb, displayRows)
	if omitted > 0 {
		sb.WriteString(fmt.Sprintf("\n*[... %d rows omitted]*\n", omitted))
	}

	outline := []OutlineItem{
		{
			Level:    1,
			Title:    "Tabular Data",
			Location: fmt.Sprintf("%d rows, %d columns", totalRows, maxCols),
		},
	}

	return &Document{
		Format:  format,
		Content: sb.String(),
		Outline: outline,
		Metadata: map[string]string{
			"rows": fmt.Sprintf("%d", totalRows),
			"cols": fmt.Sprintf("%d", maxCols),
		},
	}, nil
}
