package pdfextract

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

var (
	codeOrSyntaxRegex   = regexp.MustCompile(`(?m)(^\s*(def|class|func|import|return|package|const|var|public|private|void|interface|let|function)\b|[{};]\s*$|^\s*"[a-zA-Z0-9_-]+"\s*:|\b(console\.log|println|printf|tool_calls|arguments)\b|^\s*(\/\*|\/\/|#\s*[a-zA-Z]))`)
	sectionHeadingRegex = regexp.MustCompile(`^\s*\d+(\.\d+)+\s+`)
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
	for _, t := range latticeTables {
		if isTablePopulated(t) {
			tables = append(tables, t)
		}
	}

	var occupiedBBoxes []Rect
	for _, t := range tables {
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
	for _, t := range booktabsTables {
		if isTablePopulated(t) {
			tables = append(tables, t)
			occupiedBBoxes = append(occupiedBBoxes, Rect{
				X0: t.BBox[0],
				Y0: t.BBox[1],
				X1: t.BBox[2],
				Y1: t.BBox[3],
			})
		}
	}

	// 3. Try Stream Table Extraction only if no ruled or booktabs tables found on this page
	// and candidate cells are strictly compact tabular tokens (not multi-column prose)
	if len(tables) == 0 {
		streamTables := extractStreamTables(page, occupiedBBoxes)
		for _, t := range streamTables {
			if isTablePopulated(t) {
				tables = append(tables, t)
				occupiedBBoxes = append(occupiedBBoxes, Rect{
					X0: t.BBox[0],
					Y0: t.BBox[1],
					X1: t.BBox[2],
					Y1: t.BBox[3],
				})
			}
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
			if topLine.y-cand.y > 650.0 {
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

			var interRules []float64
			for k := i + 1; k < bottomIdx; k++ {
				interRules = append(interRules, mergedH[k].y)
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
				t := buildBooktabsTable(spansInside, interRules, tblBBox, page.Index)
				if isTablePopulated(t) {
					t.Ruled = true
					tables = append(tables, t)
					excludedBBoxes = append(excludedBBoxes, tblBBox)
					i = bottomIdx
				}
			}
		}
	}
	return tables
}

// buildBooktabsTable constructs a table bounded by horizontal rules, resolving column
// positions from text span alignment and combining multi-line text into logical rows.
func buildBooktabsTable(spans []TextSpan, interRules []float64, tblBBox Rect, pageNum int) Table {
	if len(spans) < 4 {
		return Table{}
	}

	// 1. Group spans into baseline bands
	sorted := make([]TextSpan, len(spans))
	copy(sorted, spans)
	sort.Slice(sorted, func(i, j int) bool {
		if math.Abs(sorted[i].BBox.Y0-sorted[j].BBox.Y0) > 3.0 {
			return sorted[i].BBox.Y0 > sorted[j].BBox.Y0
		}
		return sorted[i].BBox.X0 < sorted[j].BBox.X0
	})

	type tableBand struct {
		y     float64
		spans []TextSpan
	}
	var bands []tableBand
	for _, s := range sorted {
		placed := false
		for i := range bands {
			if math.Abs(bands[i].y-s.BBox.Y0) <= 3.0 {
				bands[i].spans = append(bands[i].spans, s)
				placed = true
				break
			}
		}
		if !placed {
			bands = append(bands, tableBand{y: s.BBox.Y0, spans: []TextSpan{s}})
		}
	}

	if len(bands) < 2 {
		return Table{}
	}

	// 2. Identify column start coordinates by clustering X0 positions
	var allX []float64
	for _, s := range spans {
		allX = append(allX, s.BBox.X0)
	}
	sort.Float64s(allX)
	cols := clusterFloats(allX, 20.0)
	if len(cols) < 2 {
		return Table{}
	}

	// 3. Map spans to band grid
	bandGrid := make([][]string, len(bands))
	for b, band := range bands {
		bandGrid[b] = make([]string, len(cols))
		for _, s := range band.spans {
			bestCol := 0
			bestDist := math.Abs(s.BBox.X0 - cols[0])
			for c := 1; c < len(cols); c++ {
				d := math.Abs(s.BBox.X0 - cols[c])
				if d < bestDist {
					bestDist = d
					bestCol = c
				}
			}
			if bandGrid[b][bestCol] != "" {
				bandGrid[b][bestCol] += " " + s.Text
			} else {
				bandGrid[b][bestCol] = s.Text
			}
		}
	}

	// Sort intermediate rules descending (from top of table to bottom)
	sort.Slice(interRules, func(i, j int) bool { return interRules[i] > interRules[j] })

	// Average vertical gap between baseline bands
	avgGap := 14.0
	if len(bands) >= 2 {
		totalGap := 0.0
		for i := 1; i < len(bands); i++ {
			totalGap += bands[i-1].y - bands[i].y
		}
		avgGap = totalGap / float64(len(bands)-1)
	}

	var rows [][]string
	var currentRow []string

	crossesRule := func(y1, y2 float64) bool {
		for _, rY := range interRules {
			if (y1 >= rY && y2 <= rY) || (y2 >= rY && y1 <= rY) {
				return true
			}
		}
		return false
	}

	// Check if column 0 acts as a row anchor / key
	col0Count := 0
	for b := range bands {
		if strings.TrimSpace(bandGrid[b][0]) != "" {
			col0Count++
		}
	}
	col0HasKeys := col0Count >= 2

	// Header boundary is defined by the first intermediate rule (e.g. \midrule)
	headerBoundaryY := 0.0
	if len(interRules) > 0 {
		headerBoundaryY = interRules[0]
	}

	for b := 0; b < len(bands); b++ {
		currY := bands[b].y
		isNewRow := false

		if b == 0 {
			isNewRow = true
		} else {
			prevY := bands[b-1].y
			vGap := prevY - currY

			if headerBoundaryY > 0 && prevY >= headerBoundaryY && currY < headerBoundaryY {
				// Transition from header to body
				isNewRow = true
			} else if headerBoundaryY > 0 && prevY >= headerBoundaryY && currY >= headerBoundaryY {
				// Continuation of multi-line header
				isNewRow = false
			} else if crossesRule(prevY, currY) {
				isNewRow = true
			} else if col0HasKeys && strings.TrimSpace(bandGrid[b][0]) != "" {
				isNewRow = true
			} else if vGap > avgGap*1.7 {
				isNewRow = true
			} else if !col0HasKeys {
				for c := 0; c < len(cols); c++ {
					if strings.TrimSpace(currentRow[c]) != "" && strings.TrimSpace(bandGrid[b][c]) != "" {
						isNewRow = true
						break
					}
				}
			}
		}

		if isNewRow {
			if len(currentRow) > 0 {
				rows = append(rows, currentRow)
			}
			currentRow = make([]string, len(cols))
			for c := 0; c < len(cols); c++ {
				currentRow[c] = strings.TrimSpace(bandGrid[b][c])
			}
		} else {
			for c := 0; c < len(cols); c++ {
				txt := strings.TrimSpace(bandGrid[b][c])
				if txt != "" {
					if currentRow[c] != "" {
						currentRow[c] += " " + txt
					} else {
						currentRow[c] = txt
					}
				}
			}
		}
	}
	if len(currentRow) > 0 {
		rows = append(rows, currentRow)
	}

	cleaned, _, _, _, ok := cleanAndValidateGrid(rows)
	if !ok {
		return Table{}
	}

	md := formatMarkdownTable(cleaned)
	if md == "" {
		return Table{}
	}

	return Table{
		Page:     pageNum,
		BBox:     [4]float64{tblBBox.X0, tblBBox.Y0, tblBBox.X1, tblBBox.Y1},
		Rows:     cleaned,
		Markdown: md,
		Ruled:    true,
	}
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
		if isTablePopulated(tbl) {
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

	cleanedGrid, keptCols, startRow, endRow, ok := cleanAndValidateGrid(grid)
	if !ok {
		return Table{}
	}

	bbox := [4]float64{
		xCoords[keptCols[0]],
		yCoords[endRow+1],
		xCoords[keptCols[len(keptCols)-1]+1],
		yCoords[startRow],
	}

	md := formatMarkdownTable(cleanedGrid)
	if md == "" {
		return Table{}
	}

	return Table{
		Page:     page.Index,
		BBox:     bbox,
		Rows:     cleanedGrid,
		Markdown: md,
		Ruled:    true,
	}
}

// cleanAndValidateGrid strips phantom empty columns and leading/trailing empty rows,
// then verifies that the grid has sufficient populated content to constitute a real table.
func cleanAndValidateGrid(grid [][]string) (cleaned [][]string, keptCols []int, startRow, endRow int, ok bool) {
	if len(grid) == 0 || len(grid[0]) == 0 {
		return nil, nil, 0, 0, false
	}

	numCols := len(grid[0])

	// 1. Identify columns that contain at least one cell with non-empty text.
	for c := 0; c < numCols; c++ {
		colHasText := false
		for r := range grid {
			if strings.TrimSpace(grid[r][c]) != "" {
				colHasText = true
				break
			}
		}
		if colHasText {
			keptCols = append(keptCols, c)
		}
	}
	if len(keptCols) < 2 {
		return nil, nil, 0, 0, false
	}

	// 2. Filter columns
	var colFiltered [][]string
	for r := range grid {
		row := make([]string, len(keptCols))
		for i, c := range keptCols {
			row[i] = strings.TrimSpace(grid[r][c])
		}
		colFiltered = append(colFiltered, row)
	}

	// 3. Strip leading completely empty rows
	startRow = 0
	for startRow < len(colFiltered) {
		rowHasText := false
		for _, cell := range colFiltered[startRow] {
			if cell != "" {
				rowHasText = true
				break
			}
		}
		if rowHasText {
			break
		}
		startRow++
	}

	// Strip trailing completely empty rows
	endRow = len(colFiltered) - 1
	for endRow >= startRow {
		rowHasText := false
		for _, cell := range colFiltered[endRow] {
			if cell != "" {
				rowHasText = true
				break
			}
		}
		if rowHasText {
			break
		}
		endRow--
	}

	if startRow > endRow {
		return nil, nil, 0, 0, false
	}

	cleaned = colFiltered[startRow : endRow+1]
	if len(cleaned) < 2 {
		return nil, nil, 0, 0, false
	}

	// 4. Validate content richness
	nonEmpty := 0
	rowsWithText := 0
	colsWithText := make([]bool, len(cleaned[0]))
	for _, row := range cleaned {
		hasText := false
		for c, cell := range row {
			if cell != "" {
				nonEmpty++
				hasText = true
				colsWithText[c] = true
			}
		}
		if hasText {
			rowsWithText++
		}
	}

	activeCols := 0
	for _, has := range colsWithText {
		if has {
			activeCols++
		}
	}

	// A valid table must span at least 2 rows with text and 2 columns with text
	if rowsWithText < 2 || activeCols < 2 {
		return nil, nil, 0, 0, false
	}

	totalCells := len(cleaned) * len(cleaned[0])
	minRequired := 3
	if totalCells <= 4 {
		minRequired = 2
	}
	if nonEmpty < minRequired {
		return nil, nil, 0, 0, false
	}

	// Discard large grids that are mostly empty (e.g. chart/figure grids where < 10% of cells have text)
	density := float64(nonEmpty) / float64(totalCells)
	if totalCells >= 25 && density < 0.10 {
		return nil, nil, 0, 0, false
	}

	return cleaned, keptCols, startRow, endRow, true
}

// isTablePopulated ensures a table is non-empty, properly shaped, and contains genuine tabular data.
func isTablePopulated(t Table) bool {
	if len(t.Rows) < 2 || len(t.Rows[0]) < 2 || strings.TrimSpace(t.Markdown) == "" {
		return false
	}
	nonEmpty := 0
	rowsWithText := 0
	colsWithText := make([]bool, len(t.Rows[0]))
	for _, row := range t.Rows {
		hasText := false
		for c, cell := range row {
			if strings.TrimSpace(cell) != "" {
				nonEmpty++
				hasText = true
				colsWithText[c] = true
			}
		}
		if hasText {
			rowsWithText++
		}
	}
	activeCols := 0
	for _, has := range colsWithText {
		if has {
			activeCols++
		}
	}
	return rowsWithText >= 2 && activeCols >= 2 && nonEmpty >= 2
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
			if !isCodeOrHeadingBlock(currentBlock) {
				t := buildStreamTable(currentBlock, page.Index)
				if len(t.Rows) >= 3 && len(t.Rows[0]) >= 2 {
					tables = append(tables, t)
				}
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

func isCodeOrHeadingBlock(block []multiSpanLine) bool {
	codeLines := 0
	for _, row := range block {
		txt := strings.TrimSpace(row.line.Text)
		if codeOrSyntaxRegex.MatchString(txt) || sectionHeadingRegex.MatchString(txt) {
			codeLines++
		}
	}
	return float64(codeLines)/float64(len(block)) >= 0.25
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

	cleanedGrid, _, _, _, ok := cleanAndValidateGrid(grid)
	if !ok {
		return Table{}
	}

	md := formatMarkdownTable(cleanedGrid)
	if md == "" {
		return Table{}
	}

	return Table{
		Page:     pageNum,
		BBox:     [4]float64{minX, minY, maxX, maxY},
		Rows:     cleanedGrid,
		Markdown: md,
		Ruled:    false,
	}
}

// formatMarkdownTable formats a 2D string grid into a clean Markdown table.
func formatMarkdownTable(rows [][]string) string {
	if len(rows) == 0 || len(rows[0]) == 0 {
		return ""
	}

	hasContent := false
	for _, row := range rows {
		for _, cell := range row {
			if strings.TrimSpace(cell) != "" {
				hasContent = true
				break
			}
		}
		if hasContent {
			break
		}
	}
	if !hasContent {
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
