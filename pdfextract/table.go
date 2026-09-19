package pdfextract

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Table represents an extracted structured table.
type Table struct {
	Page     int
	BBox     [4]float64 // X0, Y0, X1, Y1
	Rows     [][]string
	Markdown string
	Ruled    bool
}

// ExtractTables finds and extracts all intact tables (ruled lattice, booktabs, and borderless stream)
// on a page, returning the tables and a filtered list of text spans with table text removed.
func ExtractTables(page *ParsedPage) ([]Table, []TextSpan) {
	var tables []Table

	// 1. Try Lattice Table Extraction (vector stroke grid)
	latticeTables := extractLatticeTables(page)
	tables = append(tables, latticeTables...)

	var occupiedBBoxes []Rect
	for _, t := range latticeTables {
		occupiedBBoxes = append(occupiedBBoxes, Rect{
			X0: t.BBox[0],
			Y0: t.BBox[1],
			X1: t.BBox[2],
			Y1: t.BBox[3],
		})
	}

	// 2. Try Booktabs Table Extraction (horizontal rules without vertical lines, common in papers)
	allHLines := extractAllHLines(page)
	booktabsTables := extractBooktabsTables(page, allHLines, occupiedBBoxes)
	tables = append(tables, booktabsTables...)

	for _, t := range booktabsTables {
		occupiedBBoxes = append(occupiedBBoxes, Rect{
			X0: t.BBox[0],
			Y0: t.BBox[1],
			X1: t.BBox[2],
			Y1: t.BBox[3],
		})
	}

	// 3. Try Stream Table Extraction only if no ruled or booktabs tables found on this page
	// and candidate cells are strictly compact tabular tokens (not multi-column prose)
	if len(tables) == 0 {
		streamTables := extractStreamTables(page, occupiedBBoxes)
		tables = append(tables, streamTables...)

		for _, t := range streamTables {
			occupiedBBoxes = append(occupiedBBoxes, Rect{
				X0: t.BBox[0],
				Y0: t.BBox[1],
				X1: t.BBox[2],
				Y1: t.BBox[3],
			})
		}
	}

	// Filter out spans that fell inside any table bounding box
	var nonTableSpans []TextSpan
	for _, span := range page.Spans {
		inside := false
		for _, tb := range occupiedBBoxes {
			midX := (span.BBox.X0 + span.BBox.X1) / 2
			midY := (span.BBox.Y0 + span.BBox.Y1) / 2
			if tb.ContainsPoint(midX, midY) {
				inside = true
				break
			}
		}
		if !inside {
			nonTableSpans = append(nonTableSpans, span)
		}
	}

	return tables, nonTableSpans
}

func extractAllHLines(page *ParsedPage) []hSegment {
	var hLines []hSegment
	for _, l := range page.Lines {
		dx := math.Abs(l.End.X - l.Start.X)
		dy := math.Abs(l.End.Y - l.Start.Y)
		if dy <= 2.5 && dx >= 60.0 {
			hLines = append(hLines, hSegment{
				x0: math.Min(l.Start.X, l.End.X),
				x1: math.Max(l.Start.X, l.End.X),
				y:  (l.Start.Y + l.End.Y) / 2,
			})
		}
	}
	for _, r := range page.Rects {
		if r.BBox.Width() >= 60.0 && r.BBox.Height() >= 10.0 {
			hLines = append(hLines,
				hSegment{x0: r.BBox.X0, x1: r.BBox.X1, y: r.BBox.Y1},
				hSegment{x0: r.BBox.X0, x1: r.BBox.X1, y: r.BBox.Y0},
			)
		}
	}
	return hLines
}

// extractBooktabsTables extracts tables bounded by horizontal rules (LaTeX \toprule, \midrule, \bottomrule)
// without requiring vertical lines.
func extractBooktabsTables(page *ParsedPage, hLines []hSegment, excludedBBoxes []Rect) []Table {
	if len(hLines) < 2 {
		return nil
	}
	mergedH := mergeHorizontalLines(hLines)
	if len(mergedH) < 2 {
		return nil
	}

	sort.Slice(mergedH, func(i, j int) bool {
		return mergedH[i].y > mergedH[j].y
	})

	var tables []Table
	for i := 0; i < len(mergedH); i++ {
		topLine := mergedH[i]
		if topLine.x1-topLine.x0 < 60.0 {
			continue
		}

		bottomIdx := -1
		for j := i + 1; j < len(mergedH); j++ {
			cand := mergedH[j]
			if topLine.y-cand.y > 450.0 {
				break
			}
			if math.Abs(cand.x0-topLine.x0) <= 35.0 && math.Abs(cand.x1-topLine.x1) <= 35.0 {
				bottomIdx = j
			}
		}

		if bottomIdx > i {
			bottomLine := mergedH[bottomIdx]
			tblBBox := Rect{
				X0: math.Min(topLine.x0, bottomLine.x0) - 2.0,
				Y0: bottomLine.y - 2.0,
				X1: math.Max(topLine.x1, bottomLine.x1) + 2.0,
				Y1: topLine.y + 2.0,
			}

			overlap := false
			for _, ex := range excludedBBoxes {
				if ex.Intersects(tblBBox) {
					overlap = true
					break
				}
			}
			if overlap {
				continue
			}

			var spansInside []TextSpan
			for _, s := range page.Spans {
				midX := (s.BBox.X0 + s.BBox.X1) / 2
				midY := (s.BBox.Y0 + s.BBox.Y1) / 2
				if tblBBox.ContainsPoint(midX, midY) {
					spansInside = append(spansInside, s)
				}
			}

			if len(spansInside) >= 4 {
				lines := groupSpansIntoLines(spansInside, tblBBox)
				if len(lines) >= 2 {
					var cands []multiSpanLine
					for _, l := range lines {
						cands = append(cands, multiSpanLine{line: l, spans: l.Spans})
					}
					t := buildStreamTable(cands, page.Index)
					if len(t.Rows) >= 2 && len(t.Rows[0]) >= 2 {
						t.Ruled = true
						t.BBox = [4]float64{tblBBox.X0, tblBBox.Y0, tblBBox.X1, tblBBox.Y1}
						tables = append(tables, t)
						excludedBBoxes = append(excludedBBoxes, tblBBox)
						i = bottomIdx
					}
				}
			}
		}
	}
	return tables
}

type hSegment struct {
	x0, x1, y float64
}

type vSegment struct {
	x, y0, y1 float64
}

// extractLatticeTables detects table grids from vector lines and rectangles.
func extractLatticeTables(page *ParsedPage) []Table {
	var hLines []hSegment
	var vLines []vSegment

	// Process raw vector lines (table lines are substantial, at least 60pt wide)
	for _, l := range page.Lines {
		dx := math.Abs(l.End.X - l.Start.X)
		dy := math.Abs(l.End.Y - l.Start.Y)

		if dy <= 2.5 && dx >= 60.0 {
			hLines = append(hLines, hSegment{
				x0: math.Min(l.Start.X, l.End.X),
				x1: math.Max(l.Start.X, l.End.X),
				y:  (l.Start.Y + l.End.Y) / 2,
			})
		} else if dx <= 2.5 && dy >= 8.0 {
			vLines = append(vLines, vSegment{
				x:  (l.Start.X + l.End.X) / 2,
				y0: math.Min(l.Start.Y, l.End.Y),
				y1: math.Max(l.Start.Y, l.End.Y),
			})
		}
	}

	// Process vector rectangles into border lines
	for _, r := range page.Rects {
		if r.BBox.Width() >= 20.0 && r.BBox.Height() >= 10.0 {
			// Top and bottom horizontal lines
			hLines = append(hLines,
				hSegment{x0: r.BBox.X0, x1: r.BBox.X1, y: r.BBox.Y1},
				hSegment{x0: r.BBox.X0, x1: r.BBox.X1, y: r.BBox.Y0},
			)
			// Left and right vertical lines
			vLines = append(vLines,
				vSegment{x: r.BBox.X0, y0: r.BBox.Y0, y1: r.BBox.Y1},
				vSegment{x: r.BBox.X1, y0: r.BBox.Y0, y1: r.BBox.Y1},
			)
		}
	}

	if len(hLines) < 2 || len(vLines) < 2 {
		return nil
	}

	// Merge collinear lines
	mergedH := mergeHorizontalLines(hLines)
	mergedV := mergeVerticalLines(vLines)

	if len(mergedH) < 2 || len(mergedV) < 2 {
		return nil
	}

	// Cluster table grids: group intersecting H and V lines
	tableClusters := clusterGrids(mergedH, mergedV)
	var outTables []Table

	for _, cluster := range tableClusters {
		tbl := buildLatticeTable(cluster, page)
		if len(tbl.Rows) >= 2 && len(tbl.Rows[0]) >= 2 {
			outTables = append(outTables, tbl)
		}
	}

	return outTables
}

func mergeHorizontalLines(lines []hSegment) []hSegment {
	sort.Slice(lines, func(i, j int) bool {
		if math.Abs(lines[i].y-lines[j].y) > 2.0 {
			return lines[i].y < lines[j].y
		}
		return lines[i].x0 < lines[j].x0
	})

	var merged []hSegment
	for _, l := range lines {
		if len(merged) == 0 {
			merged = append(merged, l)
			continue
		}
		last := &merged[len(merged)-1]
		if math.Abs(last.y-l.y) <= 2.0 && l.x0 <= last.x1+3.0 {
			// Merge overlapping or touching
			last.x1 = math.Max(last.x1, l.x1)
			last.x0 = math.Min(last.x0, l.x0)
			last.y = (last.y + l.y) / 2
		} else {
			merged = append(merged, l)
		}
	}
	return merged
}

func mergeVerticalLines(lines []vSegment) []vSegment {
	sort.Slice(lines, func(i, j int) bool {
		if math.Abs(lines[i].x-lines[j].x) > 2.0 {
			return lines[i].x < lines[j].x
		}
		return lines[i].y0 < lines[j].y0
	})

	var merged []vSegment
	for _, l := range lines {
		if len(merged) == 0 {
			merged = append(merged, l)
			continue
		}
		last := &merged[len(merged)-1]
		if math.Abs(last.x-l.x) <= 2.0 && l.y0 <= last.y1+3.0 {
			// Merge overlapping or touching
			last.y1 = math.Max(last.y1, l.y1)
			last.y0 = math.Min(last.y0, l.y0)
			last.x = (last.x + l.x) / 2
		} else {
			merged = append(merged, l)
		}
	}
	return merged
}

type gridCluster struct {
	hLines []hSegment
	vLines []vSegment
	bbox   Rect
}

func clusterGrids(hLines []hSegment, vLines []vSegment) []gridCluster {
	// A simple cluster: group lines that overlap in 2D space
	var clusters []gridCluster

	for _, h := range hLines {
		var matchingV []vSegment
		for _, v := range vLines {
			// Check if h and v cross or touch
			if v.x >= h.x0-3.0 && v.x <= h.x1+3.0 && h.y >= v.y0-3.0 && h.y <= v.y1+3.0 {
				matchingV = append(matchingV, v)
			}
		}
		if len(matchingV) < 2 {
			continue
		}

		placed := false
		for i := range clusters {
			c := &clusters[i]
			// If h overlaps cluster bounding box
			if h.y >= c.bbox.Y0-10.0 && h.y <= c.bbox.Y1+10.0 &&
				!(h.x1 < c.bbox.X0 || h.x0 > c.bbox.X1) {
				c.hLines = append(c.hLines, h)
				for _, mv := range matchingV {
					c.vLines = append(c.vLines, mv)
				}
				c.bbox.X0 = math.Min(c.bbox.X0, h.x0)
				c.bbox.X1 = math.Max(c.bbox.X1, h.x1)
				c.bbox.Y0 = math.Min(c.bbox.Y0, h.y)
				c.bbox.Y1 = math.Max(c.bbox.Y1, h.y)
				placed = true
				break
			}
		}

		if !placed {
			minX, maxX := h.x0, h.x1
			minY, maxY := h.y, h.y
			for _, v := range matchingV {
				minX = math.Min(minX, v.x)
				maxX = math.Max(maxX, v.x)
				minY = math.Min(minY, v.y0)
				maxY = math.Max(maxY, v.y1)
			}
			clusters = append(clusters, gridCluster{
				hLines: []hSegment{h},
				vLines: matchingV,
				bbox:   Rect{X0: minX, Y0: minY, X1: maxX, Y1: maxY},
			})
		}
	}

	// De-duplicate lines within clusters
	for i := range clusters {
		clusters[i].hLines = mergeHorizontalLines(clusters[i].hLines)
		clusters[i].vLines = mergeVerticalLines(clusters[i].vLines)
	}

	return clusters
}

func buildLatticeTable(cluster gridCluster, page *ParsedPage) Table {
	// Extract unique sorted Y coordinates (descending: top to bottom)
	var yCoords []float64
	for _, h := range cluster.hLines {
		yCoords = append(yCoords, h.y)
	}
	sort.Slice(yCoords, func(i, j int) bool { return yCoords[i] > yCoords[j] })
	yCoords = clusterFloats(yCoords, 3.0)

	// Extract unique sorted X coordinates (ascending: left to right)
	var xCoords []float64
	for _, v := range cluster.vLines {
		xCoords = append(xCoords, v.x)
	}
	sort.Float64s(xCoords)
	xCoords = clusterFloats(xCoords, 3.0)

	numRows := len(yCoords) - 1
	numCols := len(xCoords) - 1

	if numRows < 1 || numCols < 1 {
		return Table{}
	}

	grid := make([][]string, numRows)
	for r := 0; r < numRows; r++ {
		grid[r] = make([]string, numCols)
	}

	// Assign text spans to cells
	for _, span := range page.Spans {
		midX := (span.BBox.X0 + span.BBox.X1) / 2
		midY := (span.BBox.Y0 + span.BBox.Y1) / 2

		// Find row r: where yCoords[r] >= midY >= yCoords[r+1]
		rowIdx := -1
		for r := 0; r < numRows; r++ {
			if midY <= yCoords[r]+2.0 && midY >= yCoords[r+1]-2.0 {
				rowIdx = r
				break
			}
		}

		// Find col c: where xCoords[c] <= midX <= xCoords[c+1]
		colIdx := -1
		for c := 0; c < numCols; c++ {
			if midX >= xCoords[c]-2.0 && midX <= xCoords[c+1]+2.0 {
				colIdx = c
				break
			}
		}

		if rowIdx >= 0 && colIdx >= 0 {
			if grid[rowIdx][colIdx] != "" {
				grid[rowIdx][colIdx] += " " + span.Text
			} else {
				grid[rowIdx][colIdx] = span.Text
			}
		}
	}

	// Trim cell strings
	for r := 0; r < numRows; r++ {
		for c := 0; c < numCols; c++ {
			grid[r][c] = strings.TrimSpace(grid[r][c])
		}
	}

	md := formatMarkdownTable(grid)
	return Table{
		Page:     page.Index,
		BBox:     [4]float64{cluster.bbox.X0, cluster.bbox.Y0, cluster.bbox.X1, cluster.bbox.Y1},
		Rows:     grid,
		Markdown: md,
		Ruled:    true,
	}
}

func clusterFloats(vals []float64, tol float64) []float64 {
	if len(vals) == 0 {
		return nil
	}
	var out []float64
	out = append(out, vals[0])
	for i := 1; i < len(vals); i++ {
		last := out[len(out)-1]
		if math.Abs(vals[i]-last) > tol {
			out = append(out, vals[i])
		}
	}
	return out
}

type multiSpanLine struct {
	line  TextLine
	spans []TextSpan
}

// extractStreamTables finds borderless tables by detecting recurring vertical column gutters.
func extractStreamTables(page *ParsedPage, excludedBBoxes []Rect) []Table {
	// Filter spans outside excluded bboxes
	var availSpans []TextSpan
	for _, span := range page.Spans {
		inside := false
		for _, ex := range excludedBBoxes {
			midX := (span.BBox.X0 + span.BBox.X1) / 2
			midY := (span.BBox.Y0 + span.BBox.Y1) / 2
			if ex.ContainsPoint(midX, midY) {
				inside = true
				break
			}
		}
		if !inside {
			availSpans = append(availSpans, span)
		}
	}

	if len(availSpans) < 6 {
		return nil
	}

	// Group spans into lines
	lines := groupSpansIntoLines(availSpans, page.MediaBox)
	if len(lines) < 3 {
		return nil
	}

	var candidates []multiSpanLine
	for _, l := range lines {
		if len(l.Spans) >= 2 {
			candidates = append(candidates, multiSpanLine{line: l, spans: l.Spans})
		}
	}

	if len(candidates) < 3 {
		return nil
	}

	// Group consecutive multi-span lines whose column X coordinates closely align
	var tables []Table
	var currentBlock []multiSpanLine

	flushBlock := func() {
		if len(currentBlock) >= 3 {
			t := buildStreamTable(currentBlock, page.Index)
			if len(t.Rows) >= 3 && len(t.Rows[0]) >= 2 {
				tables = append(tables, t)
			}
		}
		currentBlock = nil
	}

	for i := 0; i < len(candidates); i++ {
		cand := candidates[i]
		if len(currentBlock) == 0 {
			currentBlock = append(currentBlock, cand)
			continue
		}

		prev := currentBlock[len(currentBlock)-1]
		vGap := prev.line.BBox.Y0 - cand.line.BBox.Y1
		if vGap > prev.line.FontSize*2.0 || vGap < -2.0 {
			// Gap too large or irregular
			flushBlock()
			currentBlock = append(currentBlock, cand)
			continue
		}

		// Check if column counts and alignments roughly match
		if math.Abs(float64(len(prev.spans)-len(cand.spans))) <= 1 {
			currentBlock = append(currentBlock, cand)
		} else {
			flushBlock()
			currentBlock = append(currentBlock, cand)
		}
	}
	flushBlock()

	return tables
}

func buildStreamTable(block []multiSpanLine, pageNum int) Table {
	// Guard against running multi-column prose or math lines being misinterpreted as a borderless table.
	// Table cells are predominantly concise tokens, numbers, and short labels.
	totalChars := 0
	totalSpans := 0
	for _, row := range block {
		for _, s := range row.spans {
			trimmed := strings.TrimSpace(s.Text)
			// Running prose sentences (>= 8 words or > 60 chars) are paragraphs, not table cells
			if strings.Count(trimmed, " ") >= 8 || len(trimmed) > 60 {
				return Table{}
			}
			totalChars += len(trimmed)
			totalSpans++
		}
	}
	if totalSpans > 0 && float64(totalChars)/float64(totalSpans) > 28.0 {
		return Table{}
	}

	// Collect all X0 positions to identify column boundaries
	var allX []float64
	for _, row := range block {
		for _, s := range row.spans {
			allX = append(allX, s.BBox.X0)
		}
	}
	sort.Float64s(allX)

	// Cluster into distinct column start X coordinates
	cols := clusterFloats(allX, 20.0)
	if len(cols) < 2 {
		return Table{}
	}

	grid := make([][]string, len(block))
	minX, minY := math.MaxFloat64, math.MaxFloat64
	maxX, maxY := -math.MaxFloat64, -math.MaxFloat64

	for r, row := range block {
		grid[r] = make([]string, len(cols))
		minY = math.Min(minY, row.line.BBox.Y0)
		maxY = math.Max(maxY, row.line.BBox.Y1)
		minX = math.Min(minX, row.line.BBox.X0)
		maxX = math.Max(maxX, row.line.BBox.X1)

		for _, s := range row.spans {
			// Find closest column index
			bestCol := 0
			bestDist := math.Abs(s.BBox.X0 - cols[0])
			for c := 1; c < len(cols); c++ {
				d := math.Abs(s.BBox.X0 - cols[c])
				if d < bestDist {
					bestDist = d
					bestCol = c
				}
			}

			if grid[r][bestCol] != "" {
				grid[r][bestCol] += " " + s.Text
			} else {
				grid[r][bestCol] = s.Text
			}
		}
	}

	md := formatMarkdownTable(grid)
	return Table{
		Page:     pageNum,
		BBox:     [4]float64{minX, minY, maxX, maxY},
		Rows:     grid,
		Markdown: md,
		Ruled:    false,
	}
}

// formatMarkdownTable formats a 2D string grid into a clean Markdown table.
func formatMarkdownTable(rows [][]string) string {
	if len(rows) == 0 || len(rows[0]) == 0 {
		return ""
	}

	numCols := len(rows[0])
	colWidths := make([]int, numCols)

	// Ensure each row has numCols
	normalized := make([][]string, len(rows))
	for r := range rows {
		normalized[r] = make([]string, numCols)
		for c := 0; c < numCols; c++ {
			var val string
			if c < len(rows[r]) {
				val = sanitizeCellText(rows[r][c])
			}
			normalized[r][c] = val
			if len(val) > colWidths[c] {
				colWidths[c] = len(val)
			}
		}
	}

	for c := 0; c < numCols; c++ {
		fallbackLen := len(fmt.Sprintf("Col %d", c+1))
		if colWidths[c] < fallbackLen {
			colWidths[c] = fallbackLen
		}
	}

	var sb strings.Builder

	// Header Row
	sb.WriteString("|")
	for c := 0; c < numCols; c++ {
		val := normalized[0][c]
		if val == "" {
			val = fmt.Sprintf("Col %d", c+1)
		}
		padLen := colWidths[c] - len(val)
		if padLen < 0 {
			padLen = 0
		}
		pad := strings.Repeat(" ", padLen)
		sb.WriteString(fmt.Sprintf(" %s%s |", val, pad))
	}
	sb.WriteString("\n")

	// Separator Row
	sb.WriteString("|")
	for c := 0; c < numCols; c++ {
		sb.WriteString(fmt.Sprintf(":%s|", strings.Repeat("-", colWidths[c]+1)))
	}
	sb.WriteString("\n")

	// Data Rows
	for r := 1; r < len(normalized); r++ {
		sb.WriteString("|")
		for c := 0; c < numCols; c++ {
			val := normalized[r][c]
			padLen := colWidths[c] - len(val)
			if padLen < 0 {
				padLen = 0
			}
			pad := strings.Repeat(" ", padLen)
			sb.WriteString(fmt.Sprintf(" %s%s |", val, pad))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func sanitizeCellText(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	return strings.TrimSpace(s)
}
