package pdfextract

import (
	"strings"
	"testing"
)

func line(x0, x1, y float64, text string) TextLine {
	return TextLine{
		Text: text,
		BBox: Rect{X0: x0, Y0: y, X1: x1, Y1: y + 10},
	}
}

func TestFindLineColumnSplitRequiresTwoMargins(t *testing.T) {
	page := Rect{X0: 0, Y0: 0, X1: 612, Y1: 792}

	// A single-column page has one margin; no split.
	var single []TextLine
	for i := 0; i < 20; i++ {
		single = append(single, line(72, 540, float64(700-i*20), "body"))
	}
	if got := findLineColumnSplit(single, page); got != 0 {
		t.Errorf("single column split = %v, want 0", got)
	}

	// Two columns, each with its own left margin.
	var twoCol []TextLine
	for i := 0; i < 15; i++ {
		y := float64(700 - i*20)
		twoCol = append(twoCol, line(72, 280, y, "left"))
		twoCol = append(twoCol, line(320, 540, y, "right"))
	}
	if got := findLineColumnSplit(twoCol, page); got <= 0 {
		t.Fatalf("two-column split = %v, want a positive split", got)
	}
}

func TestFindHeaderLineYCutFindsFullWidthHeader(t *testing.T) {
	page := Rect{X0: 0, Y0: 0, X1: 612, Y1: 792}
	colSplitX := 300.0

	lines := []TextLine{
		line(72, 540, 740, "A title spanning the whole page"),
		line(72, 540, 700, "Authors across both columns"),
		line(72, 280, 650, "left column body"),
		line(320, 540, 650, "right column body"),
	}

	yCut := findHeaderLineYCut(lines, page, colSplitX)
	if yCut <= 0 {
		t.Fatalf("yCut = %v, want a positive cut below the header", yCut)
	}
	// The cut must sit below the lowest header line (Y0 = 700) and above the
	// body lines (Y0 = 650).
	if yCut > 700 || yCut < 650 {
		t.Errorf("yCut = %v, want between 650 and 700", yCut)
	}
}

func TestFindHeaderLineYCutNoHeader(t *testing.T) {
	page := Rect{X0: 0, Y0: 0, X1: 612, Y1: 792}
	lines := []TextLine{
		line(72, 280, 650, "left column body"),
		line(320, 540, 650, "right column body"),
	}
	if got := findHeaderLineYCut(lines, page, 300); got != 0 {
		t.Errorf("yCut = %v, want 0 when there is no full-width header", got)
	}
}

func TestReconstructFractionsBuildsFrac(t *testing.T) {
	spans := []TextSpan{
		{Text: "a", FontSize: 12, BBox: Rect{X0: 100, Y0: 708, X1: 108, Y1: 718}},
		{Text: "b", FontSize: 12, BBox: Rect{X0: 100, Y0: 676, X1: 108, Y1: 686}},
		{Text: "rest", FontSize: 12, BBox: Rect{X0: 300, Y0: 690, X1: 330, Y1: 700}},
	}
	lines := []VectorLine{{Start: Point{X: 96, Y: 696}, End: Point{X: 116, Y: 696}}}

	gotSpans, gotLines := ReconstructFractions(spans, lines)
	if len(gotLines) != 0 {
		t.Errorf("lines = %d, want 0 (the bar was consumed)", len(gotLines))
	}
	var frac string
	for _, s := range gotSpans {
		if strings.HasPrefix(s.Text, `\frac`) {
			frac = s.Text
		}
	}
	if frac != `\frac{a}{b}` {
		t.Fatalf("fraction = %q, want \\frac{a}{b}", frac)
	}
}

func TestReconstructFractionsIgnoresLongLines(t *testing.T) {
	spans := []TextSpan{{Text: "a", BBox: Rect{X0: 100, Y0: 708, X1: 108, Y1: 718}}}
	lines := []VectorLine{{Start: Point{X: 0, Y: 700}, End: Point{X: 500, Y: 700}}}
	gotSpans, gotLines := ReconstructFractions(spans, lines)
	if len(gotSpans) != 1 || len(gotLines) != 1 {
		t.Fatalf("got %d spans and %d lines, want the inputs unchanged", len(gotSpans), len(gotLines))
	}
}

// TestFormatMarkdownTablePadsByDisplayWidth proves CJK and emoji cells are
// padded by rendered width, not by byte length.
func TestFormatMarkdownTablePadsByDisplayWidth(t *testing.T) {
	md := formatMarkdownTable([][]string{
		{"Name", "City"},
		{"東京", "日本"},
		{"Bob", "Paris"},
	})
	rows := strings.Split(strings.TrimRight(md, "\n"), "\n")
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4:\n%s", len(rows), md)
	}

	// The header is rows[0] and rows[2:] are data (rows[1] is the separator).
	// Padding is correct only when every cell in a column renders at the same
	// display width; byte-length padding makes the CJK row shorter.
	var want = cellWidths(t, rows[0])
	for _, row := range rows[2:] {
		got := cellWidths(t, row)
		for c := range got {
			if got[c] != want[c] {
				t.Errorf("row %q: column %d renders %d wide, want %d\n%s", row, c, got[c], want[c], md)
			}
		}
	}
}

func cellWidths(t *testing.T, row string) []int {
	t.Helper()
	row = strings.TrimPrefix(row, "|")
	row = strings.TrimSuffix(row, "|")
	parts := strings.Split(row, "|")
	widths := make([]int, len(parts))
	for i, p := range parts {
		// Strip the single leading and trailing space the formatter adds,
		// keeping the padding spaces that make up the cell width.
		p = strings.TrimPrefix(p, " ")
		p = strings.TrimSuffix(p, " ")
		widths[i] = displayWidth(p)
	}
	return widths
}
