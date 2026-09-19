package docextract

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// extractDocx extracts structured Markdown and an outline from a Word .docx archive.
func extractDocx(r *zip.Reader, opts Options) (*Document, error) {
	// 1. Read relationships from word/_rels/document.xml.rels for hyperlinks and media.
	rels := make(map[string]string)
	for _, f := range r.File {
		if f.Name == "word/_rels/document.xml.rels" {
			rc, err := f.Open()
			if err == nil {
				rels = parseDocxRels(rc)
				rc.Close()
			}
			break
		}
	}

	// 2. Locate word/document.xml.
	var docFile *zip.File
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return nil, fmt.Errorf("docextract: invalid docx: missing word/document.xml")
	}

	rc, err := docFile.Open()
	if err != nil {
		return nil, fmt.Errorf("docextract: open word/document.xml: %w", err)
	}
	defer rc.Close()

	doc, err := parseDocxDocument(rc, rels, opts)
	if err != nil {
		return nil, err
	}
	doc.Format = FormatDocx
	return doc, nil
}

func parseDocxRels(r io.Reader) map[string]string {
	rels := make(map[string]string)
	decoder := xml.NewDecoder(r)
	decoder.Strict = false
	for {
		t, err := decoder.Token()
		if err != nil {
			break
		}
		if se, ok := t.(xml.StartElement); ok {
			if se.Name.Local == "Relationship" {
				var id, target string
				for _, attr := range se.Attr {
					if attr.Name.Local == "Id" {
						id = attr.Value
					} else if attr.Name.Local == "Target" {
						target = attr.Value
					}
				}
				if id != "" && target != "" {
					rels[id] = target
				}
			}
		}
	}
	return rels
}

type docxRun struct {
	text      string
	bold      bool
	italic    bool
	strike    bool
	code      bool
	hyperlink string
}

type docxParagraph struct {
	style     string
	level     int // 1-6 for headings, 0 for regular
	isList    bool
	listLevel int
	runs      []docxRun
}

func (p *docxParagraph) text() string {
	var sb strings.Builder
	for _, r := range p.runs {
		txt := r.text
		if txt == "" {
			continue
		}
		if r.code {
			txt = "`" + txt + "`"
		}
		if r.bold && r.italic {
			txt = "***" + txt + "***"
		} else if r.bold {
			txt = "**" + txt + "**"
		} else if r.italic {
			txt = "*" + txt + "*"
		}
		if r.strike {
			txt = "~~" + txt + "~~"
		}
		if r.hyperlink != "" {
			txt = fmt.Sprintf("[%s](%s)", txt, r.hyperlink)
		}
		sb.WriteString(txt)
	}
	return sb.String()
}

type docxTableFrame struct {
	table   [][]string
	curRow  []string
	curCell strings.Builder
}

func parseDocxDocument(r io.Reader, rels map[string]string, opts Options) (*Document, error) {
	decoder := xml.NewDecoder(r)
	decoder.Strict = false

	var (
		paragraphs   []string
		outlineItems []OutlineItem
		metadata     = make(map[string]string)
		title        string

		// Table stack handles nested tables cleanly
		tableStack []docxTableFrame

		// Paragraph state
		curPara      docxParagraph
		curRun       docxRun
		inRunPr      bool
		inParaPr     bool
		inHyperlink  bool
		curLinkID    string
		inText       bool
		textBuf      strings.Builder
		inDrawing    bool
		drawingDescr string
		drawingName  string
	)

	flushParagraph := func() {
		raw := curPara.text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" && drawingDescr == "" && drawingName == "" {
			curPara = docxParagraph{}
			drawingDescr = ""
			drawingName = ""
			return
		}

		var line string
		if curPara.level > 0 {
			prefix := strings.Repeat("#", curPara.level) + " "
			line = prefix + trimmed
			outlineItems = append(outlineItems, OutlineItem{
				Level:    curPara.level,
				Title:    trimmed,
				Location: fmt.Sprintf("Heading %d", curPara.level),
			})
			if title == "" && curPara.level == 1 {
				title = trimmed
			}
		} else if curPara.isList {
			indent := strings.Repeat("  ", curPara.listLevel)
			line = indent + "- " + trimmed
		} else {
			line = trimmed
		}

		if drawingDescr != "" || drawingName != "" {
			imgLabel := drawingDescr
			if imgLabel == "" {
				imgLabel = drawingName
			}
			imgMd := fmt.Sprintf("![%s](image)", imgLabel)
			if line != "" {
				line += "\n\n" + imgMd
			} else {
				line = imgMd
			}
		}

		if len(tableStack) > 0 {
			top := &tableStack[len(tableStack)-1]
			if top.curCell.Len() > 0 {
				top.curCell.WriteString(" ")
			}
			top.curCell.WriteString(line)
		} else {
			paragraphs = append(paragraphs, line)
		}

		curPara = docxParagraph{}
		drawingDescr = ""
		drawingName = ""
	}

	flushCell := func() {
		if len(tableStack) > 0 {
			top := &tableStack[len(tableStack)-1]
			top.curRow = append(top.curRow, strings.TrimSpace(top.curCell.String()))
			top.curCell.Reset()
		}
	}

	flushRow := func() {
		if len(tableStack) > 0 {
			top := &tableStack[len(tableStack)-1]
			if len(top.curRow) > 0 {
				top.table = append(top.table, top.curRow)
				top.curRow = nil
			}
		}
	}

	flushTable := func() {
		if len(tableStack) == 0 {
			return
		}
		top := tableStack[len(tableStack)-1]
		tableStack = tableStack[:len(tableStack)-1]
		if len(top.table) == 0 {
			return
		}
		// Render GFM Table
		var sb strings.Builder
		maxCols := 0
		for _, row := range top.table {
			if len(row) > maxCols {
				maxCols = len(row)
			}
		}
		if maxCols == 0 {
			return
		}

		for rowIndex, row := range top.table {
			sb.WriteString("|")
			for colIndex := 0; colIndex < maxCols; colIndex++ {
				val := ""
				if colIndex < len(row) {
					val = strings.ReplaceAll(row[colIndex], "|", `\|`)
					val = strings.ReplaceAll(val, "\n", "<br>")
				}
				sb.WriteString(" " + strings.TrimSpace(val) + " |")
			}
			sb.WriteString("\n")

			// Add separator after header row
			if rowIndex == 0 {
				sb.WriteString("|")
				for colIndex := 0; colIndex < maxCols; colIndex++ {
					sb.WriteString(" --- |")
				}
				sb.WriteString("\n")
			}
		}
		tblStr := strings.TrimRight(sb.String(), "\n")
		if len(tableStack) > 0 {
			parent := &tableStack[len(tableStack)-1]
			if parent.curCell.Len() > 0 {
				parent.curCell.WriteString("<br>")
			}
			parent.curCell.WriteString(tblStr)
		} else {
			paragraphs = append(paragraphs, tblStr)
		}
	}

	for {
		tok, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("docextract: xml decode: %w", err)
		}

		switch elem := tok.(type) {
		case xml.StartElement:
			name := elem.Name.Local
			switch name {
			case "tbl":
				tableStack = append(tableStack, docxTableFrame{})
			case "tr":
				if len(tableStack) > 0 {
					tableStack[len(tableStack)-1].curRow = nil
				}
			case "tc":
				if len(tableStack) > 0 {
					tableStack[len(tableStack)-1].curCell.Reset()
				}
			case "p":
				curPara = docxParagraph{}
			case "pPr":
				inParaPr = true
			case "pStyle":
				if inParaPr {
					for _, a := range elem.Attr {
						if a.Name.Local == "val" {
							curPara.style = a.Value
							curPara.level = headingLevelFromStyle(a.Value)
						}
					}
				}
			case "numPr":
				if inParaPr {
					curPara.isList = true
				}
			case "ilvl":
				if inParaPr {
					for _, a := range elem.Attr {
						if a.Name.Local == "val" {
							var lvl int
							fmt.Sscanf(a.Value, "%d", &lvl)
							curPara.listLevel = lvl
						}
					}
				}
			case "hyperlink":
				inHyperlink = true
				for _, a := range elem.Attr {
					if a.Name.Local == "id" {
						curLinkID = a.Value
					}
				}
			case "r":
				curRun = docxRun{}
				if inHyperlink && curLinkID != "" {
					curRun.hyperlink = rels[curLinkID]
				}
			case "rPr":
				inRunPr = true
			case "b":
				if inRunPr && !isAttrValFalse(elem.Attr) {
					curRun.bold = true
				}
			case "i":
				if inRunPr && !isAttrValFalse(elem.Attr) {
					curRun.italic = true
				}
			case "strike":
				if inRunPr && !isAttrValFalse(elem.Attr) {
					curRun.strike = true
				}
			case "rFonts":
				if inRunPr {
					for _, a := range elem.Attr {
						if a.Name.Local == "ascii" || a.Name.Local == "hAnsi" {
							v := strings.ToLower(a.Value)
							if strings.Contains(v, "courier") || strings.Contains(v, "consolas") ||
								strings.Contains(v, "mono") || strings.Contains(v, "menlo") {
								curRun.code = true
							}
						}
					}
				}
			case "t":
				inText = true
				textBuf.Reset()
			case "tab":
				curRun.text += "\t"
			case "br":
				curRun.text += "\n"
			case "drawing":
				inDrawing = true
			case "docPr":
				if inDrawing {
					for _, a := range elem.Attr {
						if a.Name.Local == "descr" && a.Value != "" {
							drawingDescr = a.Value
						} else if a.Name.Local == "name" && a.Value != "" {
							drawingName = a.Value
						} else if a.Name.Local == "title" && a.Value != "" {
							if drawingDescr == "" {
								drawingDescr = a.Value
							}
						}
					}
				}
			}

		case xml.EndElement:
			name := elem.Name.Local
			switch name {
			case "tbl":
				flushTable()
			case "tr":
				flushRow()
			case "tc":
				flushCell()
			case "pPr":
				inParaPr = false
			case "p":
				flushParagraph()
			case "hyperlink":
				inHyperlink = false
				curLinkID = ""
			case "rPr":
				inRunPr = false
			case "t":
				inText = false
				curRun.text += textBuf.String()
			case "r":
				if curRun.text != "" {
					curPara.runs = append(curPara.runs, curRun)
				}
				curRun = docxRun{}
			case "drawing":
				inDrawing = false
			}

		case xml.CharData:
			if inText {
				textBuf.Write(elem)
			}
		}
	}

	content := strings.Join(paragraphs, "\n\n")

	// If query filter is specified, filter content to the matching section
	if opts.Query != "" {
		filtered := extractSectionByQuery(content, opts.Query)
		if filtered != "" {
			content = filtered
		}
	}

	return &Document{
		Title:    title,
		Content:  content,
		Outline:  outlineItems,
		Metadata: metadata,
	}, nil
}

func headingLevelFromStyle(style string) int {
	s := strings.ToLower(strings.TrimSpace(style))
	switch s {
	case "title":
		return 1
	case "subtitle":
		return 2
	case "heading1", "heading 1", "h1":
		return 1
	case "heading2", "heading 2", "h2":
		return 2
	case "heading3", "heading 3", "h3":
		return 3
	case "heading4", "heading 4", "h4":
		return 4
	case "heading5", "heading 5", "h5":
		return 5
	case "heading6", "heading 6", "h6":
		return 6
	}
	// Try prefix match e.g. "heading 1", "heading2"
	if strings.HasPrefix(s, "heading") {
		numStr := strings.TrimPrefix(s, "heading")
		numStr = strings.TrimSpace(numStr)
		if len(numStr) == 1 && numStr[0] >= '1' && numStr[0] <= '6' {
			return int(numStr[0] - '0')
		}
	}
	return 0
}

func isAttrValFalse(attrs []xml.Attr) bool {
	for _, a := range attrs {
		if a.Name.Local == "val" {
			v := strings.ToLower(a.Value)
			return v == "0" || v == "false" || v == "none" || v == "off"
		}
	}
	return false
}
