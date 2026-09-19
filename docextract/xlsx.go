package docextract

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"unicode"
)

type xlsxSheetInfo struct {
	Name    string
	SheetID string
	RelID   string
	Path    string
}

func extractXlsx(r *zip.Reader, opts Options) (*Document, error) {
	// 1. Parse xl/workbook.xml for sheet names and r:ids
	var wbFile *zip.File
	for _, f := range r.File {
		if f.Name == "xl/workbook.xml" {
			wbFile = f
			break
		}
	}
	if wbFile == nil {
		return nil, fmt.Errorf("docextract: invalid xlsx: missing xl/workbook.xml")
	}

	rc, err := wbFile.Open()
	if err != nil {
		return nil, fmt.Errorf("docextract: open xl/workbook.xml: %w", err)
	}
	sheets, err := parseXlsxWorkbook(rc)
	rc.Close()
	if err != nil {
		return nil, err
	}

	// 2. Parse xl/_rels/workbook.xml.rels for target paths
	rels := make(map[string]string)
	for _, f := range r.File {
		if f.Name == "xl/_rels/workbook.xml.rels" {
			rc, err := f.Open()
			if err == nil {
				rels = parseDocxRels(rc)
				rc.Close()
			}
			break
		}
	}

	for i := range sheets {
		target := rels[sheets[i].RelID]
		if target != "" {
			if !strings.HasPrefix(target, "/") {
				target = path.Join("xl", target)
			} else {
				target = strings.TrimPrefix(target, "/")
			}
			sheets[i].Path = target
		} else {
			sheets[i].Path = fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1)
		}
	}

	// 3. Parse shared strings if present
	var sharedStrings []string
	for _, f := range r.File {
		if f.Name == "xl/sharedStrings.xml" {
			rc, err := f.Open()
			if err == nil {
				sharedStrings = parseXlsxSharedStrings(rc)
				rc.Close()
			}
			break
		}
	}

	// 4. Filter sheets if query or pages specified
	filterSheet := opts.Query
	filterPages := opts.Pages

	var (
		outlineItems []OutlineItem
		sections     []string
	)

	maxRows := opts.MaxRowsPerSheet
	if maxRows <= 0 {
		maxRows = 200 // default preview limit per sheet
	}

	for sheetIdx, s := range sheets {
		// Check sheet filter
		if filterSheet != "" {
			if !strings.EqualFold(s.Name, filterSheet) && !strings.Contains(strings.ToLower(s.Name), strings.ToLower(filterSheet)) {
				continue
			}
		}
		if filterPages != "" {
			// pages could be "1", "1-2", "Sheet1"
			if !matchPageOrName(sheetIdx+1, s.Name, filterPages) {
				continue
			}
		}

		var sheetFile *zip.File
		for _, f := range r.File {
			if f.Name == s.Path {
				sheetFile = f
				break
			}
		}
		if sheetFile == nil {
			continue
		}

		rc, err := sheetFile.Open()
		if err != nil {
			continue
		}
		grid, totalRows, totalCols, err := parseXlsxWorksheet(rc, sharedStrings, maxRows)
		rc.Close()
		if err != nil {
			continue
		}

		outlineItems = append(outlineItems, OutlineItem{
			Level:    1,
			Title:    s.Name,
			Location: fmt.Sprintf("Sheet %d (%d rows, %d cols)", sheetIdx+1, totalRows, totalCols),
		})

		// Format sheet as Markdown table
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("## Sheet: %s\n\n", s.Name))
		if len(grid) == 0 {
			sb.WriteString("*Empty sheet*\n")
		} else {
			renderGridAsMarkdownTable(&sb, grid)
			if totalRows > len(grid) {
				sb.WriteString(fmt.Sprintf("\n*[... %d rows omitted]*\n", totalRows-len(grid)))
			}
		}
		sections = append(sections, sb.String())
	}

	doc := &Document{
		Format:   FormatXlsx,
		Outline:  outlineItems,
		Metadata: map[string]string{"total_sheets": strconv.Itoa(len(sheets))},
	}
	if len(sheets) > 0 {
		doc.Title = sheets[0].Name
	}
	doc.Content = strings.Join(sections, "\n\n")
	return doc, nil
}

func parseXlsxWorkbook(r io.Reader) ([]xlsxSheetInfo, error) {
	var sheets []xlsxSheetInfo
	decoder := xml.NewDecoder(r)
	decoder.Strict = false
	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok {
			if se.Name.Local == "sheet" {
				var s xlsxSheetInfo
				for _, a := range se.Attr {
					switch a.Name.Local {
					case "name":
						s.Name = a.Value
					case "sheetId":
						s.SheetID = a.Value
					case "id": // r:id
						s.RelID = a.Value
					}
				}
				if s.Name != "" {
					sheets = append(sheets, s)
				}
			}
		}
	}
	return sheets, nil
}

func parseXlsxSharedStrings(r io.Reader) []string {
	var (
		stringsList []string
		decoder     = xml.NewDecoder(r)
		inText      bool
		curStr      strings.Builder
		inSI        bool
	)
	decoder.Strict = false

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		switch elem := tok.(type) {
		case xml.StartElement:
			if elem.Name.Local == "si" {
				inSI = true
				curStr.Reset()
			} else if elem.Name.Local == "t" {
				inText = true
			}
		case xml.EndElement:
			if elem.Name.Local == "si" {
				stringsList = append(stringsList, curStr.String())
				inSI = false
			} else if elem.Name.Local == "t" {
				inText = false
			}
		case xml.CharData:
			if inText && inSI {
				curStr.Write(elem)
			}
		}
	}
	return stringsList
}

func parseXlsxWorksheet(r io.Reader, sharedStrings []string, maxRows int) (grid [][]string, totalRows, totalCols int, err error) {
	decoder := xml.NewDecoder(r)
	decoder.Strict = false

	var (
		cellType  string
		cellRef   string
		inVal     bool
		valBuf    strings.Builder
		inInline  bool
		inlineBuf strings.Builder

		curRowCells = make(map[int]string)
		maxColIndex = -1
		curRowIdx   = -1
	)

	flushRow := func() {
		if curRowIdx < 0 || len(curRowCells) == 0 {
			curRowCells = make(map[int]string)
			return
		}
		totalRows++

		if len(grid) < maxRows {
			rowSlice := make([]string, maxColIndex+1)
			for cIdx, v := range curRowCells {
				if cIdx < len(rowSlice) {
					rowSlice[cIdx] = v
				}
			}
			grid = append(grid, rowSlice)
		}
		curRowCells = make(map[int]string)
	}

	for {
		tok, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, 0, 0, err
		}

		switch elem := tok.(type) {
		case xml.StartElement:
			switch elem.Name.Local {
			case "row":
				flushRow()
				curRowIdx = -1
				for _, a := range elem.Attr {
					if a.Name.Local == "r" {
						curRowIdx, _ = strconv.Atoi(a.Value)
					}
				}
			case "c":
				cellType = ""
				cellRef = ""
				valBuf.Reset()
				inlineBuf.Reset()
				for _, a := range elem.Attr {
					if a.Name.Local == "r" {
						cellRef = a.Value
					} else if a.Name.Local == "t" {
						cellType = a.Value
					}
				}
			case "v":
				inVal = true
				valBuf.Reset()
			case "is":
				inInline = true
				inlineBuf.Reset()
			}

		case xml.EndElement:
			switch elem.Name.Local {
			case "row":
				flushRow()
				curRowIdx = -1
			case "c":
				rawVal := strings.TrimSpace(valBuf.String())
				var finalVal string
				switch cellType {
				case "s": // shared string
					idx, _ := strconv.Atoi(rawVal)
					if idx >= 0 && idx < len(sharedStrings) {
						finalVal = sharedStrings[idx]
					} else {
						finalVal = rawVal
					}
				case "inlineStr":
					finalVal = inlineBuf.String()
				case "b":
					if rawVal == "1" {
						finalVal = "TRUE"
					} else {
						finalVal = "FALSE"
					}
				default:
					finalVal = rawVal
				}

				if cellRef != "" {
					colIdx, _, ok := parseCellRef(cellRef)
					if ok {
						if colIdx > maxColIndex {
							maxColIndex = colIdx
						}
						curRowCells[colIdx] = finalVal
					}
				}
			case "v":
				inVal = false
			case "is":
				inInline = false
			}

		case xml.CharData:
			if inVal {
				valBuf.Write(elem)
			} else if inInline {
				inlineBuf.Write(elem)
			}
		}
	}
	flushRow()

	totalCols = maxColIndex + 1
	return grid, totalRows, totalCols, nil
}

func parseCellRef(ref string) (colIdx int, rowIdx int, ok bool) {
	lettersEnd := 0
	for lettersEnd < len(ref) && unicode.IsLetter(rune(ref[lettersEnd])) {
		lettersEnd++
	}
	if lettersEnd == 0 || lettersEnd == len(ref) {
		return 0, 0, false
	}
	colLetters := strings.ToUpper(ref[:lettersEnd])
	rowStr := ref[lettersEnd:]

	idx := 0
	for i := 0; i < len(colLetters); i++ {
		idx = idx*26 + int(colLetters[i]-'A'+1)
	}
	colIdx = idx - 1
	rowIdx, err := strconv.Atoi(rowStr)
	if err != nil {
		return 0, 0, false
	}
	return colIdx, rowIdx, true
}

func renderGridAsMarkdownTable(sb *strings.Builder, grid [][]string) {
	if len(grid) == 0 {
		return
	}
	maxCols := 0
	for _, row := range grid {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}
	if maxCols == 0 {
		return
	}

	for rowIndex, row := range grid {
		sb.WriteString("|")
		for colIndex := 0; colIndex < maxCols; colIndex++ {
			val := ""
			if colIndex < len(row) {
				val = strings.ReplaceAll(row[colIndex], "|", `\|`)
				val = strings.ReplaceAll(val, "\n", " ")
			}
			sb.WriteString(" " + strings.TrimSpace(val) + " |")
		}
		sb.WriteString("\n")

		// After first row, emit header separator
		if rowIndex == 0 {
			sb.WriteString("|")
			for colIndex := 0; colIndex < maxCols; colIndex++ {
				sb.WriteString(" --- |")
			}
			sb.WriteString("\n")
		}
	}
}

func matchPageOrName(pageIndex int, name, filter string) bool {
	if strings.EqualFold(name, filter) {
		return true
	}
	// Check if filter is single number e.g. "2"
	if num, err := strconv.Atoi(filter); err == nil {
		return num == pageIndex
	}
	// Check range e.g. "1-3"
	if parts := strings.Split(filter, "-"); len(parts) == 2 {
		start, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		end, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 == nil && err2 == nil {
			return pageIndex >= start && pageIndex <= end
		}
	}
	return false
}
