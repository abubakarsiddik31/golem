package docextract

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
)

type pptxSlideRef struct {
	Index int
	RelID string
	Path  string
}

func extractPptx(r *zip.Reader, opts Options) (*Document, error) {
	// 1. Locate and parse ppt/presentation.xml
	var presFile *zip.File
	for _, f := range r.File {
		if f.Name == "ppt/presentation.xml" {
			presFile = f
			break
		}
	}
	if presFile == nil {
		return nil, fmt.Errorf("docextract: invalid pptx: missing ppt/presentation.xml")
	}

	rc, err := presFile.Open()
	if err != nil {
		return nil, fmt.Errorf("docextract: open ppt/presentation.xml: %w", err)
	}
	slideRelIDs, err := parsePptxPresentation(rc)
	rc.Close()
	if err != nil {
		return nil, err
	}

	// 2. Locate and parse ppt/_rels/presentation.xml.rels
	rels := make(map[string]string)
	for _, f := range r.File {
		if f.Name == "ppt/_rels/presentation.xml.rels" {
			rc, err := f.Open()
			if err == nil {
				rels = parseDocxRels(rc)
				rc.Close()
			}
			break
		}
	}

	var slides []pptxSlideRef
	for i, relID := range slideRelIDs {
		target := rels[relID]
		if target != "" {
			if !strings.HasPrefix(target, "/") {
				target = path.Join("ppt", target)
			} else {
				target = strings.TrimPrefix(target, "/")
			}
			slides = append(slides, pptxSlideRef{
				Index: i + 1,
				RelID: relID,
				Path:  target,
			})
		} else {
			slides = append(slides, pptxSlideRef{
				Index: i + 1,
				RelID: relID,
				Path:  fmt.Sprintf("ppt/slides/slide%d.xml", i+1),
			})
		}
	}

	var (
		outlineItems []OutlineItem
		sections     []string
		firstTitle   string
	)

	for _, s := range slides {
		// Filter by pages (e.g. "1", "1-3")
		if opts.Pages != "" && !matchPageOrName(s.Index, "", opts.Pages) {
			continue
		}

		var slideFile *zip.File
		for _, f := range r.File {
			if f.Name == s.Path {
				slideFile = f
				break
			}
		}
		if slideFile == nil {
			continue
		}

		rc, err := slideFile.Open()
		if err != nil {
			continue
		}
		slideTitle, slideBody, err := parsePptxSlide(rc)
		rc.Close()
		if err != nil {
			continue
		}

		if slideTitle == "" {
			slideTitle = fmt.Sprintf("Slide %d", s.Index)
		}
		if firstTitle == "" {
			firstTitle = slideTitle
		}

		// Filter by query if specified
		if opts.Query != "" {
			matched := strings.Contains(strings.ToLower(slideTitle), strings.ToLower(opts.Query)) ||
				strings.Contains(strings.ToLower(slideBody), strings.ToLower(opts.Query))
			if !matched {
				continue
			}
		}

		outlineItems = append(outlineItems, OutlineItem{
			Level:    1,
			Title:    slideTitle,
			Location: fmt.Sprintf("Slide %d", s.Index),
		})

		// Check for speaker notes in ppt/slides/_rels/slideX.xml.rels
		notes := extractPptxSpeakerNotes(r, s.Path)

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("## Slide %d: %s\n\n", s.Index, slideTitle))
		if strings.TrimSpace(slideBody) != "" {
			sb.WriteString(strings.TrimSpace(slideBody))
			sb.WriteString("\n")
		}
		if notes != "" {
			sb.WriteString("\n> **Speaker Notes:**\n")
			for _, nLine := range strings.Split(notes, "\n") {
				sb.WriteString("> " + nLine + "\n")
			}
		}
		sections = append(sections, sb.String())
	}

	doc := &Document{
		Format:   FormatPptx,
		Title:    firstTitle,
		Content:  strings.Join(sections, "\n\n"),
		Outline:  outlineItems,
		Metadata: map[string]string{"total_slides": strconv.Itoa(len(slides))},
	}
	return doc, nil
}

func parsePptxPresentation(r io.Reader) ([]string, error) {
	var slideRelIDs []string
	decoder := xml.NewDecoder(r)
	decoder.Strict = false
	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok {
			if se.Name.Local == "sldId" {
				relID := ""
				for _, a := range se.Attr {
					if strings.HasPrefix(a.Value, "rId") {
						relID = a.Value
						break
					}
					if a.Name.Space != "" && a.Name.Local == "id" {
						relID = a.Value
						break
					}
				}
				if relID != "" {
					slideRelIDs = append(slideRelIDs, relID)
				}
			}
		}
	}
	return slideRelIDs, nil
}

func parsePptxSlide(r io.Reader) (title string, body string, err error) {
	decoder := xml.NewDecoder(r)
	decoder.Strict = false

	var (
		isTitleShape bool
		inTxBody     bool
		paraLvl      int
		runBold      bool
		runItalic    bool
		inText       bool
		textBuf      strings.Builder

		curParaBuf strings.Builder
		bodyLines  []string
		titleLines []string

		// Table support
		inTable   bool
		tableGrid [][]string
		curRow    []string
		curCell   strings.Builder
	)

	flushPara := func() {
		raw := strings.TrimSpace(curParaBuf.String())
		curParaBuf.Reset()
		if raw == "" {
			return
		}

		if inTable {
			if curCell.Len() > 0 {
				curCell.WriteString(" ")
			}
			curCell.WriteString(raw)
			return
		}

		if isTitleShape {
			titleLines = append(titleLines, raw)
		} else {
			var line string
			if paraLvl > 0 {
				indent := strings.Repeat("  ", paraLvl)
				line = indent + "- " + raw
			} else {
				line = "- " + raw
			}
			bodyLines = append(bodyLines, line)
		}
	}

	for {
		tok, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", "", err
		}

		switch elem := tok.(type) {
		case xml.StartElement:
			switch elem.Name.Local {
			case "sp":
				isTitleShape = false
			case "ph":
				for _, a := range elem.Attr {
					if a.Name.Local == "type" {
						val := strings.ToLower(a.Value)
						if val == "title" || val == "ctrtitle" || val == "subttl" {
							isTitleShape = true
						}
					}
				}
			case "txBody":
				inTxBody = true
			case "p":
				if inTxBody {
					paraLvl = 0
					curParaBuf.Reset()
				}
			case "pPr":
				for _, a := range elem.Attr {
					if a.Name.Local == "lvl" {
						lvl, _ := strconv.Atoi(a.Value)
						paraLvl = lvl
					}
				}
			case "r":
				runBold = false
				runItalic = false
			case "rPr":
				for _, a := range elem.Attr {
					if a.Name.Local == "b" && a.Value == "1" {
						runBold = true
					} else if a.Name.Local == "i" && a.Value == "1" {
						runItalic = true
					}
				}
			case "t":
				inText = true
				textBuf.Reset()
			case "tbl":
				inTable = true
				tableGrid = nil
			case "tr":
				curRow = nil
			case "tc":
				curCell.Reset()
			}

		case xml.EndElement:
			switch elem.Name.Local {
			case "sp":
				isTitleShape = false
			case "txBody":
				inTxBody = false
			case "p":
				if inTxBody {
					flushPara()
				}
			case "t":
				inText = false
				txt := textBuf.String()
				if runBold && runItalic {
					txt = "***" + txt + "***"
				} else if runBold {
					txt = "**" + txt + "**"
				} else if runItalic {
					txt = "*" + txt + "*"
				}
				curParaBuf.WriteString(txt)
			case "tbl":
				inTable = false
				var tblBuf strings.Builder
				renderGridAsMarkdownTable(&tblBuf, tableGrid)
				if tblBuf.Len() > 0 {
					bodyLines = append(bodyLines, tblBuf.String())
				}
				tableGrid = nil
			case "tr":
				if len(curRow) > 0 {
					tableGrid = append(tableGrid, curRow)
					curRow = nil
				}
			case "tc":
				curRow = append(curRow, strings.TrimSpace(curCell.String()))
				curCell.Reset()
			}

		case xml.CharData:
			if inText {
				textBuf.Write(elem)
			}
		}
	}

	title = strings.Join(titleLines, " ")
	if title == "" && len(bodyLines) > 0 {
		// If no explicit title shape, use first bullet line as title
		first := strings.TrimPrefix(bodyLines[0], "- ")
		title = first
		bodyLines = bodyLines[1:]
	}

	body = strings.Join(bodyLines, "\n")
	return title, body, nil
}

func extractPptxSpeakerNotes(r *zip.Reader, slidePath string) string {
	dir, file := path.Split(slidePath)
	relsPath := path.Join(dir, "_rels", file+".rels")

	var relsFile *zip.File
	for _, f := range r.File {
		if f.Name == relsPath {
			relsFile = f
			break
		}
	}
	if relsFile == nil {
		return ""
	}

	rc, err := relsFile.Open()
	if err != nil {
		return ""
	}
	rels := parseDocxRels(rc)
	rc.Close()

	var notesPath string
	for _, target := range rels {
		if strings.Contains(target, "notesSlide") {
			notesPath = path.Clean(path.Join(dir, target))
			break
		}
	}
	if notesPath == "" {
		return ""
	}

	var notesFile *zip.File
	for _, f := range r.File {
		if f.Name == notesPath {
			notesFile = f
			break
		}
	}
	if notesFile == nil {
		return ""
	}

	nrc, err := notesFile.Open()
	if err != nil {
		return ""
	}
	defer nrc.Close()

	// Parse notes slide text
	var (
		decoder  = xml.NewDecoder(nrc)
		inText   bool
		textBuf  strings.Builder
		curPara  strings.Builder
		lines    []string
		isHeader bool
	)
	decoder.Strict = false

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		switch elem := tok.(type) {
		case xml.StartElement:
			if elem.Name.Local == "ph" {
				for _, a := range elem.Attr {
					if a.Name.Local == "type" && (a.Value == "sldNum" || a.Value == "hdr") {
						isHeader = true
					}
				}
			} else if elem.Name.Local == "t" {
				inText = true
				textBuf.Reset()
			} else if elem.Name.Local == "p" {
				curPara.Reset()
				isHeader = false
			}
		case xml.EndElement:
			if elem.Name.Local == "t" {
				inText = false
				curPara.WriteString(textBuf.String())
			} else if elem.Name.Local == "p" {
				pText := strings.TrimSpace(curPara.String())
				if pText != "" && !isHeader {
					lines = append(lines, pText)
				}
			}
		case xml.CharData:
			if inText {
				textBuf.Write(elem)
			}
		}
	}

	return strings.Join(lines, "\n")
}
