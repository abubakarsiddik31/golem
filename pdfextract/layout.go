package pdfextract

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// BlockType indicates the semantic type of a layout element.
type BlockType int

const (
	BlockParagraph BlockType = iota
	BlockHeading
	BlockList
	BlockTable
	BlockImage
)

// LayoutBlock represents a recognized semantic block in reading order.
type LayoutBlock struct {
	Type     BlockType
	Level    int // 1, 2, 3 for headings
	Text     string
	BBox     Rect
	PageNum  int
	TableRef *Table
	ImageRef *ImageRef
}

// TextLine represents a horizontally grouped set of text spans on a common baseline.
type TextLine struct {
	Spans    []TextSpan
	Text     string
	BBox     Rect
	FontSize float64
	FontName string
}

var (
	bulletRegex      = regexp.MustCompile(`^([•\-\*–—\x{2022}\x{25CF}\x{25CB}\x{25AA}\x{2219}]|\([a-zA-Z0-9]+\))\s+`)
	orderedRegex     = regexp.MustCompile(`^\d+[\.\)]\s+`)
	numberedHeading1 = regexp.MustCompile(`^\d+\s+[A-Z].*`)
	numberedHeading2 = regexp.MustCompile(`^\d+\.\d+\s+[A-Z].*`)
	numberedHeading3 = regexp.MustCompile(`^\d+\.\d+\.\d+\s+[A-Z].*`)
	abstractRegex    = regexp.MustCompile(`^(?i)(abstract|introduction|conclusion|references|acknowledgements?|appendix)\b.*`)
	displayMathRegex = regexp.MustCompile(`(?:\([0-9]+\)\s*$|^\s*(?:\$\$|\\\[))`)
	equationNumRegex = regexp.MustCompile(`\s*\((\d+([a-z]|\.\d+)*)\)\s*$`)
)

func isDisplayMath(text string) bool {
	if strings.Contains(text, "=") || strings.Contains(text, "\\approx") || strings.Contains(text, "\\le") || strings.Contains(text, "\\ge") || strings.Contains(text, "\\sim") {
		if strings.Contains(text, "\\frac") || strings.Contains(text, "\\sqrt") ||
			strings.Contains(text, "\\sum") || strings.Contains(text, "\\in") ||
			strings.Contains(text, "\\times") || strings.Contains(text, "^") ||
			strings.Contains(text, "_") || equationNumRegex.MatchString(text) {
			return true
		}
	}
	return false
}

func isMathFont(name string) bool {
	l := strings.ToLower(name)
	return strings.Contains(l, "cmmi") || strings.Contains(l, "cmsy") ||
		strings.Contains(l, "cmex") || strings.Contains(l, "msam") ||
		strings.Contains(l, "msbm") || strings.Contains(l, "math") ||
		strings.Contains(l, "symbol")
}

// AnalyzePageLayout groups spans into lines, performs XY-Cut multi-column segmentation,
// detects heading hierarchies and lists, and outputs blocks in proper reading order.
func AnalyzePageLayout(page *ParsedPage, tables []Table, images []ImageRef) []LayoutBlock {
	if len(page.Spans) == 0 && len(tables) == 0 && len(images) == 0 {
		return nil
	}

	lines := groupSpansIntoLines(page.Spans, page.MediaBox)
	contentLines := filterMargins(lines, page.MediaBox)
	bodyFontSize := calculateMedianFontSize(contentLines)

	colSplitX := findLineColumnSplit(contentLines, page.MediaBox)
	var orderedBlocks []LayoutBlock

	if colSplitX > 0 {
		yCut := findHeaderLineYCut(contentLines, page.MediaBox, colSplitX)
		var headerLines, leftLines, rightLines []TextLine

		for _, l := range contentLines {
			if yCut > 0 && l.BBox.Y0 >= yCut {
				headerLines = append(headerLines, l)
			} else {
				midX := (l.BBox.X0 + l.BBox.X1) / 2
				if midX < colSplitX {
					leftLines = append(leftLines, l)
				} else {
					rightLines = append(rightLines, l)
				}
			}
		}

		if len(headerLines) > 0 {
			orderedBlocks = append(orderedBlocks, segmentParagraphs(headerLines, bodyFontSize)...)
		}
		orderedBlocks = append(orderedBlocks, segmentParagraphs(leftLines, bodyFontSize)...)
		orderedBlocks = append(orderedBlocks, segmentParagraphs(rightLines, bodyFontSize)...)
	} else {
		orderedBlocks = recursiveXYCut(contentLines, page.MediaBox, bodyFontSize)
	}

	// Merge table and image blocks into the layout flow
	allBlocks := mergeVisualElements(orderedBlocks, tables, images, page.Index)

	return allBlocks
}

// findLineColumnSplit identifies multi-column pages where two distinct column margins exist.
func findLineColumnSplit(lines []TextLine, mediaBox Rect) float64 {
	pageW := mediaBox.Width()
	if pageW < 250 || len(lines) < 15 {
		return 0
	}

	maxCol1X := mediaBox.X0 + pageW*0.35
	minCol2X := mediaBox.X0 + pageW*0.45
	maxCol2X := mediaBox.X0 + pageW*0.75

	leftMarginLines := 0
	rightMarginLines := 0

	for _, l := range lines {
		x0 := l.BBox.X0
		if x0 <= maxCol1X {
			leftMarginLines++
		} else if x0 >= minCol2X && x0 <= maxCol2X {
			rightMarginLines++
		}
	}

	if leftMarginLines >= 12 && rightMarginLines >= 12 {
		return (maxCol1X + minCol2X) / 2
	}
	return 0
}

// findHeaderLineYCut detects a full-width header (Title, Authors) above multi-column body text.
func findHeaderLineYCut(lines []TextLine, mediaBox Rect, colSplitX float64) float64 {
	pageW := mediaBox.Width()
	lowestHeaderY := math.MaxFloat64
	hasHeader := false
	for _, l := range lines {
		if l.BBox.Y0 > mediaBox.Y0+mediaBox.Height()*0.50 {
			if l.BBox.Width() > pageW*0.50 || (l.BBox.X0 < colSplitX-30.0 && l.BBox.X1 > colSplitX+30.0) {
				hasHeader = true
				if l.BBox.Y0 < lowestHeaderY {
					lowestHeaderY = l.BBox.Y0
				}
			}
		}
	}
	if hasHeader && lowestHeaderY < math.MaxFloat64 {
		return lowestHeaderY - 5.0
	}
	return 0
}

// ReconstructFractions detects algebraic fraction lines in vector graphics and groups
// their numerator and denominator spans into unified \frac{numerator}{denominator} text spans.
func ReconstructFractions(spans []TextSpan, lines []VectorLine) ([]TextSpan, []VectorLine) {
	if len(spans) == 0 || len(lines) == 0 {
		return spans, lines
	}

	consumedSpanIdx := make(map[int]bool)
	consumedLineIdx := make(map[int]bool)
	var newFractionSpans []TextSpan

	for lIdx, l := range lines {
		dy := math.Abs(l.End.Y - l.Start.Y)
		dx := math.Abs(l.End.X - l.Start.X)

		// Fraction bar is a short horizontal stroke
		if dy <= 1.5 && dx >= 6.0 && dx <= 140.0 {
			barY := (l.Start.Y + l.End.Y) / 2
			barX0 := math.Min(l.Start.X, l.End.X) - 3.0
			barX1 := math.Max(l.Start.X, l.End.X) + 3.0

			var numIndices, denIndices []int
			for sIdx, s := range spans {
				if consumedSpanIdx[sIdx] {
					continue
				}
				midX := (s.BBox.X0 + s.BBox.X1) / 2

				if midX >= barX0 && midX <= barX1 {
					// Numerator baseline sits above the fraction bar
					if s.BBox.Y0 >= barY-1.0 && s.BBox.Y0 <= barY+18.0 {
						numIndices = append(numIndices, sIdx)
					} else if s.BBox.Y0 < barY-1.0 && s.BBox.Y1 >= barY-18.0 {
						// Denominator baseline sits below the fraction bar
						denIndices = append(denIndices, sIdx)
					}
				}
			}

			if len(numIndices) > 0 && len(denIndices) > 0 {
				sort.Slice(numIndices, func(i, j int) bool {
					return spans[numIndices[i]].BBox.X0 < spans[numIndices[j]].BBox.X0
				})
				sort.Slice(denIndices, func(i, j int) bool {
					return spans[denIndices[i]].BBox.X0 < spans[denIndices[j]].BBox.X0
				})

				var numSB, denSB strings.Builder
				baseNumSize := spans[numIndices[0]].FontSize
				for idx, sIdx := range numIndices {
					s := spans[sIdx]
					clean := CleanText(s.Text)
					if idx > 0 && s.FontSize < baseNumSize*0.88 {
						clean = "^" + clean
					}
					numSB.WriteString(clean)
					consumedSpanIdx[sIdx] = true
				}
				baseDenSize := spans[denIndices[0]].FontSize
				for idx, sIdx := range denIndices {
					s := spans[sIdx]
					clean := CleanText(s.Text)
					if idx > 0 && s.FontSize < baseDenSize*0.88 {
						clean = "_" + clean
					}
					denSB.WriteString(clean)
					consumedSpanIdx[sIdx] = true
				}

				consumedLineIdx[lIdx] = true
				fracText := fmt.Sprintf("\\frac{%s}{%s}", strings.TrimSpace(numSB.String()), strings.TrimSpace(denSB.String()))

				newFractionSpans = append(newFractionSpans, TextSpan{
					Text:     fracText,
					FontName: spans[numIndices[0]].FontName,
					FontSize: spans[numIndices[0]].FontSize,
					BBox:     Rect{X0: barX0 + 3.0, Y0: barY - 4.0, X1: barX1 - 3.0, Y1: barY + 8.0},
					Matrix:   spans[numIndices[0]].Matrix,
				})
			}
		}
	}

	if len(newFractionSpans) == 0 {
		return spans, lines
	}

	var remainingSpans []TextSpan
	for idx, s := range spans {
		if !consumedSpanIdx[idx] {
			remainingSpans = append(remainingSpans, s)
		}
	}
	remainingSpans = append(remainingSpans, newFractionSpans...)

	var remainingLines []VectorLine
	for idx, l := range lines {
		if !consumedLineIdx[idx] {
			remainingLines = append(remainingLines, l)
		}
	}

	return remainingSpans, remainingLines
}

// groupSpansIntoLines clusters text spans that share approximately the same Y baseline.
func groupSpansIntoLines(spans []TextSpan, mediaBox Rect) []TextLine {
	if len(spans) == 0 {
		return nil
	}

	// Sort spans primarily by descending Y (top of page first), then ascending X
	sorted := make([]TextSpan, len(spans))
	copy(sorted, spans)
	sort.Slice(sorted, func(i, j int) bool {
		if math.Abs(sorted[i].BBox.Y0-sorted[j].BBox.Y0) > 3.0 {
			return sorted[i].BBox.Y0 > sorted[j].BBox.Y0 // higher Y is higher on page
		}
		return sorted[i].BBox.X0 < sorted[j].BBox.X0
	})

	var lines []TextLine
	for _, span := range sorted {
		placed := false
		for i := range lines {
			line := &lines[i]
			// Check if span is vertically aligned with this line (within tolerance)
			yDiff := math.Abs(span.BBox.Y0 - line.BBox.Y0)
			tol := math.Max(2.5, line.FontSize*0.35)

			var hGap float64
			if span.BBox.X0 >= line.BBox.X1 {
				hGap = span.BBox.X0 - line.BBox.X1
			} else if line.BBox.X0 >= span.BBox.X1 {
				hGap = line.BBox.X0 - span.BBox.X1
			} else {
				hGap = 0
			}

			// Words on the same line are within normal word spacing (<= 14pt).
			// Gaps >= 14pt indicate column gutters and must not be merged.
			hProximate := hGap <= 14.0

			// Allow subscripts, superscripts, and math formula parts to attach if horizontally proximate
			isSubOrSuper := hProximate && (span.FontSize < line.FontSize*0.88 || isMathFont(span.FontName) || isMathFont(line.FontName)) && yDiff <= line.FontSize*0.85

			if (yDiff <= tol || isSubOrSuper) && hProximate {
				line.Spans = append(line.Spans, span)
				// Expand bounding box
				line.BBox.X0 = math.Min(line.BBox.X0, span.BBox.X0)
				line.BBox.Y0 = math.Min(line.BBox.Y0, span.BBox.Y0)
				line.BBox.X1 = math.Max(line.BBox.X1, span.BBox.X1)
				line.BBox.Y1 = math.Max(line.BBox.Y1, span.BBox.Y1)
				placed = true
				break
			}
		}
		if !placed {
			lines = append(lines, TextLine{
				Spans:    []TextSpan{span},
				BBox:     span.BBox,
				FontSize: span.FontSize,
				FontName: span.FontName,
			})
		}
	}

	// Finalize line texts by sorting spans horizontally
	for i := range lines {
		l := &lines[i]
		sort.Slice(l.Spans, func(a, b int) bool {
			return l.Spans[a].BBox.X0 < l.Spans[b].BBox.X0
		})
		var sb strings.Builder
		var maxFont float64
		baselineY := l.Spans[0].BBox.Y0
		for _, sp := range l.Spans {
			if sp.FontSize > maxFont {
				maxFont = sp.FontSize
				baselineY = sp.BBox.Y0
			}
		}

		for idx, s := range l.Spans {
			clean := CleanText(s.Text)
			if clean == "" {
				continue
			}

			// Format sub/superscripts relative to primary baseline
			isSuper := s.FontSize < maxFont*0.88 && s.BBox.Y0 > baselineY+1.2
			isSub := s.FontSize < maxFont*0.88 && s.BBox.Y0 < baselineY-0.8

			formatted := clean
			if isSuper {
				if len(clean) == 1 {
					formatted = "^" + clean
				} else {
					formatted = "^{" + clean + "}"
				}
			} else if isSub {
				if len(clean) == 1 {
					formatted = "_" + clean
				} else {
					formatted = "_{" + clean + "}"
				}
			}

			if idx > 0 {
				prev := l.Spans[idx-1]
				gap := s.BBox.X0 - prev.BBox.X1
				// Add space unless adjacent, punctuation, or attached sub/superscript
				isWordSeparated := gap > s.FontSize*0.06 || (s.BBox.X0 > prev.BBox.X0+float64(len(prev.Text))*prev.FontSize*0.28 && gap > -s.FontSize*0.5)
				if !isSuper && !isSub && isWordSeparated &&
					!strings.HasSuffix(sb.String(), " ") &&
					!strings.HasPrefix(formatted, " ") &&
					!strings.HasPrefix(formatted, ",") &&
					!strings.HasPrefix(formatted, ".") &&
					!strings.HasPrefix(formatted, ")") &&
					!strings.HasPrefix(formatted, ";") &&
					!strings.HasPrefix(formatted, ":") &&
					!strings.HasPrefix(formatted, "!") &&
					!strings.HasPrefix(formatted, "?") &&
					!strings.HasPrefix(formatted, "]") &&
					!strings.HasPrefix(formatted, "}") &&
					!strings.HasSuffix(sb.String(), "(") &&
					!strings.HasSuffix(sb.String(), "[") &&
					!strings.HasSuffix(sb.String(), "{") &&
					!strings.HasSuffix(sb.String(), "_") &&
					!strings.HasSuffix(sb.String(), "^") {
					sb.WriteString(" ")
				}
			}
			sb.WriteString(formatted)
			if s.FontSize > maxFont {
				maxFont = s.FontSize
				l.FontName = s.FontName
			}
		}
		l.Text = strings.TrimSpace(sb.String())
		l.FontSize = maxFont
	}

	// Sort lines top to bottom (descending Y), breaking ties left to right (ascending X)
	sort.Slice(lines, func(i, j int) bool {
		if math.Abs(lines[i].BBox.Y0-lines[j].BBox.Y0) > 2.0 {
			return lines[i].BBox.Y0 > lines[j].BBox.Y0
		}
		return lines[i].BBox.X0 < lines[j].BBox.X0
	})

	return lines
}

// filterMargins removes running headers and footers that sit in the page margins.
func filterMargins(lines []TextLine, mediaBox Rect) []TextLine {
	pageH := mediaBox.Height()
	if pageH <= 0 {
		pageH = 792 // US letter default
	}
	topMargin := mediaBox.Y1 - pageH*0.04
	bottomMargin := mediaBox.Y0 + pageH*0.04

	var kept []TextLine
	for _, l := range lines {
		// If line is in extreme top margin and short, or extreme bottom and looks like page number
		if l.BBox.Y0 > topMargin && len(l.Text) < 50 {
			continue
		}
		if l.BBox.Y1 < bottomMargin && len(l.Text) < 20 {
			continue
		}
		kept = append(kept, l)
	}
	return kept
}

func calculateMedianFontSize(lines []TextLine) float64 {
	if len(lines) == 0 {
		return 12.0
	}
	type sizeWeight struct {
		size   float64
		weight int
	}
	var weights []sizeWeight
	totalWeight := 0
	for _, l := range lines {
		w := len(strings.TrimSpace(l.Text))
		if w <= 0 {
			w = 1
		}
		if l.FontSize > 0 {
			weights = append(weights, sizeWeight{size: l.FontSize, weight: w})
			totalWeight += w
		}
	}
	if len(weights) == 0 || totalWeight == 0 {
		return 12.0
	}
	sort.Slice(weights, func(i, j int) bool {
		return weights[i].size < weights[j].size
	})
	half := totalWeight / 2
	cum := 0
	for _, sw := range weights {
		cum += sw.weight
		if cum >= half {
			return sw.size
		}
	}
	return weights[len(weights)-1].size
}

// recursiveXYCut decomposes lines into columns (X-cuts) and paragraph blocks (Y-cuts).
func recursiveXYCut(lines []TextLine, bounds Rect, bodyFontSize float64) []LayoutBlock {
	if len(lines) == 0 {
		return nil
	}

	// Step 0: Check for mixed layout (horizontal split between full-width header/abstract and 2-column body)
	if yCut := findMixedLayoutYCut(lines, bounds); yCut > 0 {
		var topLines, bottomLines []TextLine
		for _, l := range lines {
			if l.BBox.Y0 >= yCut {
				topLines = append(topLines, l)
			} else {
				bottomLines = append(bottomLines, l)
			}
		}
		topBounds := Rect{X0: bounds.X0, Y0: yCut, X1: bounds.X1, Y1: bounds.Y1}
		bottomBounds := Rect{X0: bounds.X0, Y0: bounds.Y0, X1: bounds.X1, Y1: yCut}

		topBlocks := recursiveXYCut(topLines, topBounds, bodyFontSize)
		bottomBlocks := recursiveXYCut(bottomLines, bottomBounds, bodyFontSize)
		return append(topBlocks, bottomBlocks...)
	}

	// Step 1: Detect vertical column gap (X-cut)
	colSplitX := findColumnSplit(lines, bounds)
	if colSplitX > 0 {
		var leftLines, rightLines []TextLine
		for _, l := range lines {
			// Center of line determines which column it belongs to
			midX := (l.BBox.X0 + l.BBox.X1) / 2
			if midX < colSplitX {
				leftLines = append(leftLines, l)
			} else {
				rightLines = append(rightLines, l)
			}
		}

		leftBounds := Rect{X0: bounds.X0, Y0: bounds.Y0, X1: colSplitX, Y1: bounds.Y1}
		rightBounds := Rect{X0: colSplitX, Y0: bounds.Y0, X1: bounds.X1, Y1: bounds.Y1}

		leftBlocks := recursiveXYCut(leftLines, leftBounds, bodyFontSize)
		rightBlocks := recursiveXYCut(rightLines, rightBounds, bodyFontSize)

		// Column 1 completely before Column 2!
		return append(leftBlocks, rightBlocks...)
	}

	// Step 2: If single column, segment lines into paragraph blocks by horizontal gaps (Y-cut)
	return segmentParagraphs(lines, bodyFontSize)
}

// findMixedLayoutYCut detects a horizontal boundary between a full-width section
// (e.g. Title, Authors, Abstract) and a multi-column body.
func findMixedLayoutYCut(lines []TextLine, bounds Rect) float64 {
	if len(lines) < 6 {
		return 0
	}
	boundsW := bounds.Width()
	// Lines are sorted descending by Y
	for i := 0; i < len(lines)-1; i++ {
		curr := lines[i]
		next := lines[i+1]
		vGap := curr.BBox.Y0 - next.BBox.Y1
		if vGap >= 10.0 {
			hasWideAbove := false
			for k := 0; k <= i; k++ {
				if lines[k].BBox.Width() > boundsW*0.50 {
					hasWideAbove = true
					break
				}
			}
			narrowBelow := 0
			for k := i + 1; k < len(lines) && k <= i+6; k++ {
				if lines[k].BBox.Width() < boundsW*0.58 {
					narrowBelow++
				}
			}
			if hasWideAbove && narrowBelow >= 2 {
				return (curr.BBox.Y0 + next.BBox.Y1) / 2
			}
		}
	}
	return 0
}

// findColumnSplit checks for a prominent vertical whitespace valley dividing multiple columns.
func findColumnSplit(lines []TextLine, bounds Rect) float64 {
	if len(lines) < 4 {
		return 0
	}

	boundsW := bounds.Width()
	if boundsW < 200 {
		return 0 // Too narrow to have columns
	}

	// Count how many lines cross each X coordinate
	numBins := 100
	binWidth := boundsW / float64(numBins)
	bins := make([]int, numBins)

	for _, l := range lines {
		startBin := int((l.BBox.X0 - bounds.X0) / binWidth)
		endBin := int((l.BBox.X1 - bounds.X0) / binWidth)
		if startBin < 0 {
			startBin = 0
		}
		if endBin >= numBins {
			endBin = numBins - 1
		}
		for b := startBin; b <= endBin; b++ {
			bins[b]++
		}
	}

	// Look for a valley between 25% and 75% of page width
	minBin := int(float64(numBins) * 0.25)
	maxBin := int(float64(numBins) * 0.75)

	// Allow for 1 or 2 spanning lines (e.g. title or section headers) in the valley
	maxValleyCount := int(math.Max(2.0, float64(len(lines))*0.06))

	bestValleyBin := -1
	bestValleyWidth := 0
	currValleyStart := -1

	for b := minBin; b <= maxBin; b++ {
		if bins[b] <= maxValleyCount {
			if currValleyStart < 0 {
				currValleyStart = b
			}
		} else {
			if currValleyStart >= 0 {
				width := b - currValleyStart
				if width > bestValleyWidth {
					bestValleyWidth = width
					bestValleyBin = currValleyStart + width/2
				}
				currValleyStart = -1
			}
		}
	}

	if currValleyStart >= 0 {
		width := (maxBin + 1) - currValleyStart
		if width > bestValleyWidth {
			bestValleyWidth = width
			bestValleyBin = currValleyStart + width/2
		}
	}

	// Require valley width >= 12 pt
	minValleyBins := int(12.0 / binWidth)
	if minValleyBins < 2 {
		minValleyBins = 2
	}

	if bestValleyWidth >= minValleyBins && bestValleyBin > 0 {
		return bounds.X0 + float64(bestValleyBin)*binWidth
	}

	return 0
}

// segmentParagraphs clusters vertically adjacent lines into paragraphs or headings.
func segmentParagraphs(lines []TextLine, bodyFontSize float64) []LayoutBlock {
	if len(lines) == 0 {
		return nil
	}

	// Sort top-to-bottom, breaking ties left-to-right
	sort.Slice(lines, func(i, j int) bool {
		if math.Abs(lines[i].BBox.Y0-lines[j].BBox.Y0) > 2.0 {
			return lines[i].BBox.Y0 > lines[j].BBox.Y0
		}
		return lines[i].BBox.X0 < lines[j].BBox.X0
	})

	var blocks []LayoutBlock
	var currLines []TextLine

	flushBlock := func() {
		if len(currLines) == 0 {
			return
		}
		block := classifyAndBuildBlock(currLines, bodyFontSize)
		blocks = append(blocks, block)
		currLines = nil
	}

	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if len(currLines) == 0 {
			currLines = append(currLines, l)
			continue
		}

		prev := currLines[len(currLines)-1]
		// Vertical gap between bottom of prev and top of current
		vGap := prev.BBox.Y0 - l.BBox.Y1
		lineHeight := math.Max(prev.FontSize, l.FontSize)

		// New paragraph if vertical gap is significant (> 1.4x line height),
		// or if font size changes significantly, or if current line is a bullet/heading
		isHeading := l.FontSize >= bodyFontSize*1.15
		isPrevHeading := prev.FontSize >= bodyFontSize*1.15
		isBullet := bulletRegex.MatchString(l.Text) || orderedRegex.MatchString(l.Text)

		if vGap > lineHeight*0.6 || isHeading != isPrevHeading || isHeading || isBullet {
			flushBlock()
		}
		currLines = append(currLines, l)
	}
	flushBlock()

	return blocks
}

// classifyAndBuildBlock determines if lines form a Heading, List, or Paragraph.
func classifyAndBuildBlock(lines []TextLine, bodyFontSize float64) LayoutBlock {
	var sb strings.Builder
	var minX, minY, maxX, maxY float64
	minX, minY = math.MaxFloat64, math.MaxFloat64
	maxX, maxY = -math.MaxFloat64, -math.MaxFloat64

	maxFont := 0.0
	isBold := false

	for i, l := range lines {
		if i > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString(l.Text)
		minX = math.Min(minX, l.BBox.X0)
		minY = math.Min(minY, l.BBox.Y0)
		maxX = math.Max(maxX, l.BBox.X1)
		maxY = math.Max(maxY, l.BBox.Y1)
		if l.FontSize > maxFont {
			maxFont = l.FontSize
			if strings.Contains(strings.ToLower(l.FontName), "bold") {
				isBold = true
			}
		}
	}

	rawText := strings.TrimSpace(sb.String())
	rawText = CleanText(rawText)
	rawText = RepairHyphenation(rawText)
	bbox := Rect{X0: minX, Y0: minY, X1: maxX, Y1: maxY}

	// Generalized Display Math detection: mathematical relation with math operators or equation numbers
	if isDisplayMath(rawText) {
		eqText := rawText
		if m := equationNumRegex.FindStringSubmatch(eqText); len(m) >= 2 {
			tag := m[1]
			eqText = strings.TrimSpace(equationNumRegex.ReplaceAllString(eqText, ""))
			eqText = fmt.Sprintf("%s \\tag{%s}", eqText, tag)
		}
		if !strings.HasPrefix(eqText, "$$") {
			eqText = "$$\n" + eqText + "\n$$"
		}
		return LayoutBlock{
			Type: BlockParagraph,
			Text: eqText,
			BBox: bbox,
		}
	}

	// Numbered Heading detection (e.g. 1 Introduction, 3.2 Attention, 3.2.1 Scaled Dot-Product)
	if len(rawText) < 100 {
		if numberedHeading3.MatchString(rawText) {
			return LayoutBlock{Type: BlockHeading, Level: 3, Text: rawText, BBox: bbox}
		}
		if numberedHeading2.MatchString(rawText) {
			return LayoutBlock{Type: BlockHeading, Level: 2, Text: rawText, BBox: bbox}
		}
		if numberedHeading1.MatchString(rawText) {
			return LayoutBlock{Type: BlockHeading, Level: 1, Text: rawText, BBox: bbox}
		}
		if abstractRegex.MatchString(rawText) && len(rawText) < 40 {
			return LayoutBlock{Type: BlockHeading, Level: 2, Text: rawText, BBox: bbox}
		}
	}

	// Heading detection
	if maxFont >= bodyFontSize*1.4 || (isBold && maxFont >= bodyFontSize*1.25) {
		return LayoutBlock{
			Type:  BlockHeading,
			Level: 1,
			Text:  rawText,
			BBox:  bbox,
		}
	}
	if maxFont >= bodyFontSize*1.2 || (isBold && maxFont >= bodyFontSize*1.1) {
		return LayoutBlock{
			Type:  BlockHeading,
			Level: 2,
			Text:  rawText,
			BBox:  bbox,
		}
	}
	if maxFont >= bodyFontSize*1.08 && isBold {
		return LayoutBlock{
			Type:  BlockHeading,
			Level: 3,
			Text:  rawText,
			BBox:  bbox,
		}
	}

	// List item detection
	if bulletRegex.MatchString(rawText) || orderedRegex.MatchString(rawText) {
		return LayoutBlock{
			Type: BlockList,
			Text: rawText,
			BBox: bbox,
		}
	}

	return LayoutBlock{
		Type: BlockParagraph,
		Text: rawText,
		BBox: bbox,
	}
}

// mergeVisualElements weaves tables and images into the text layout flow by page position,
// preserving the reading order established by XY-Cut for text blocks.
func mergeVisualElements(textBlocks []LayoutBlock, tables []Table, images []ImageRef, pageNum int) []LayoutBlock {
	if len(tables) == 0 && len(images) == 0 {
		return textBlocks
	}

	result := make([]LayoutBlock, len(textBlocks))
	copy(result, textBlocks)

	type visualItem struct {
		block LayoutBlock
		bbox  Rect
	}
	var visuals []visualItem

	for i := range tables {
		t := &tables[i]
		if t.Page == pageNum {
			block := LayoutBlock{
				Type:     BlockTable,
				TableRef: t,
				Text:     t.Markdown,
				PageNum:  pageNum,
			}
			visuals = append(visuals, visualItem{
				block: block,
				bbox:  Rect{X0: t.BBox[0], Y0: t.BBox[1], X1: t.BBox[2], Y1: t.BBox[3]},
			})
		}
	}

	for i := range images {
		img := &images[i]
		if img.Page == pageNum {
			if img.IsPageScan {
				continue
			}
			block := LayoutBlock{
				Type:     BlockImage,
				ImageRef: img,
				Text:     fmt.Sprintf("![%s](%s)", img.Caption, img.Path),
				BBox:     Rect{X0: img.X0, Y0: img.Y0, X1: img.X1, Y1: img.Y1},
				PageNum:  pageNum,
			}
			visuals = append(visuals, visualItem{
				block: block,
				bbox:  block.BBox,
			})
		}
	}

	for _, vis := range visuals {
		inserted := false
		for idx, b := range result {
			if b.BBox.Y0 < vis.bbox.Y0 && (b.BBox.X0 <= vis.bbox.X1 && b.BBox.X1 >= vis.bbox.X0) {
				result = append(result[:idx], append([]LayoutBlock{vis.block}, result[idx:]...)...)
				inserted = true
				break
			}
		}
		if !inserted {
			result = append(result, vis.block)
		}
	}

	return result
}
