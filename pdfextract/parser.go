package pdfextract

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Point represents a 2D coordinate in PDF points (1/72 inch).
type Point struct {
	X, Y float64
}

// Rect represents an axis-aligned bounding box in PDF points.
type Rect struct {
	X0, Y0, X1, Y1 float64
}

// Width returns the horizontal extent of the bounding box.
func (r Rect) Width() float64 {
	return math.Abs(r.X1 - r.X0)
}

// Height returns the vertical extent of the bounding box.
func (r Rect) Height() float64 {
	return math.Abs(r.Y1 - r.Y0)
}

// Area returns the rectangular area.
func (r Rect) Area() float64 {
	return r.Width() * r.Height()
}

// Intersects reports whether r and other overlap.
func (r Rect) Intersects(other Rect) bool {
	return !(r.X1 < other.X0 || r.X0 > other.X1 || r.Y1 < other.Y0 || r.Y0 > other.Y1)
}

// ContainsPoint reports whether (x, y) is within r.
func (r Rect) ContainsPoint(x, y float64) bool {
	return x >= r.X0 && x <= r.X1 && y >= r.Y0 && y <= r.Y1
}

// TextSpan represents an extracted string with its position and styling.
type TextSpan struct {
	Text     string
	FontName string
	FontSize float64
	BBox     Rect
	Matrix   [6]float64
}

// VectorLine represents a stroked line segment in page coordinates.
type VectorLine struct {
	Start Point
	End   Point
}

// VectorRect represents a stroked or filled rectangle in page coordinates.
type VectorRect struct {
	BBox   Rect
	Stroke bool
	Fill   bool
}

// PageImage represents an embedded image placed on a page.
type PageImage struct {
	Name   string
	BBox   Rect
	Format string // "jpeg", "png", "raw"
	Width  int
	Height int
	Data   []byte
}

// ParsedPage holds all raw extracted elements from a single PDF page.
type ParsedPage struct {
	Index    int
	MediaBox Rect
	Spans    []TextSpan
	Lines    []VectorLine
	Rects    []VectorRect
	Images   []PageImage
}

// Matrix represents a 3x3 2D affine transformation matrix:
// [ a  b  0 ]
// [ c  d  0 ]
// [ e  f  1 ]
type Matrix [6]float64

// IdentityMatrix returns the 2D identity matrix.
func IdentityMatrix() Matrix {
	return Matrix{1, 0, 0, 1, 0, 0}
}

// Multiply computes m * other.
func (m Matrix) Multiply(o Matrix) Matrix {
	return Matrix{
		m[0]*o[0] + m[1]*o[2],
		m[0]*o[1] + m[1]*o[3],
		m[2]*o[0] + m[3]*o[2],
		m[2]*o[1] + m[3]*o[3],
		m[4]*o[0] + m[5]*o[2] + o[4],
		m[4]*o[1] + m[5]*o[3] + o[5],
	}
}

// Transform maps point (x, y) through m.
func (m Matrix) Transform(x, y float64) Point {
	return Point{
		X: x*m[0] + y*m[2] + m[4],
		Y: x*m[1] + y*m[3] + m[5],
	}
}

// PDF object types
type (
	pdfNull struct{}
	pdfBool bool
	pdfInt  int64
	pdfReal float64
	pdfName string
	pdfStr  []byte
	pdfRef  struct {
		Num uint32
		Gen uint16
	}
	pdfArray  []any
	pdfDict   map[string]any
	pdfStream struct {
		Dict pdfDict
		Data []byte
	}
)

type objStmLoc struct {
	stmObjNum uint32
	index     int
}

// PDFDoc holds a parsed PDF document structure.
type PDFDoc struct {
	data         []byte
	objects      map[pdfRef]any
	xrefOffsets  map[uint32]int64
	objStmLocs   map[uint32]objStmLoc
	loadedObjStm map[uint32]bool
	pages        []pdfDict
	root         pdfDict
	rootRef      any
}

// ParsePDF parses raw PDF bytes into a PDFDoc structure.
func ParsePDF(data []byte) (*PDFDoc, error) {
	if len(data) < 8 || !bytes.HasPrefix(data, []byte("%PDF-")) {
		// Attempt to find %PDF- marker within the first 1024 bytes (some files have junk headers)
		idx := bytes.Index(data, []byte("%PDF-"))
		if idx < 0 {
			return nil, errors.New("pdfextract: invalid PDF header: missing %PDF-")
		}
		data = data[idx:]
	}

	doc := &PDFDoc{
		data:         data,
		objects:      make(map[pdfRef]any),
		xrefOffsets:  make(map[uint32]int64),
		objStmLocs:   make(map[uint32]objStmLoc),
		loadedObjStm: make(map[uint32]bool),
	}

	if err := doc.readXRefsAndTrailer(); err != nil {
		// Fallback: full linear scan for indirect objects
		if scanErr := doc.linearScanObjects(); scanErr != nil {
			return nil, fmt.Errorf("pdfextract: failed to parse PDF objects: %w", err)
		}
	}

	if err := doc.resolvePages(); err != nil {
		return nil, fmt.Errorf("pdfextract: failed to resolve PDF pages: %w", err)
	}

	return doc, nil
}

// NumPages returns the count of resolved pages.
func (d *PDFDoc) NumPages() int {
	return len(d.pages)
}

// readXRefsAndTrailer locates startxref and parses xref tables and trailers.
func (d *PDFDoc) readXRefsAndTrailer() error {
	startXRef := d.findStartXRef()
	if startXRef < 0 || startXRef >= int64(len(d.data)) {
		return errors.New("pdfextract: startxref not found or invalid")
	}

	currOffset := startXRef
	seenOffsets := make(map[int64]bool)

	for currOffset > 0 && currOffset < int64(len(d.data)) && !seenOffsets[currOffset] {
		seenOffsets[currOffset] = true
		p := newParser(d.data[currOffset:])
		tok, err := p.nextNonSpaceToken()
		if err != nil {
			break
		}

		if tok == "xref" {
			// Classic xref table
			if err := d.parseClassicXRef(p); err != nil {
				break
			}
			// Read trailer
			trailerTok, err := p.nextNonSpaceToken()
			if err != nil || trailerTok != "trailer" {
				break
			}
			trailerObj, err := p.parseObject()
			if err != nil {
				break
			}
			if dict, ok := trailerObj.(pdfDict); ok {
				if rootRef, hasRoot := dict["Root"]; hasRoot && d.rootRef == nil {
					d.rootRef = rootRef
				}
				if prev, hasPrev := dict["Prev"]; hasPrev {
					if prevInt, ok := toInt(prev); ok {
						currOffset = prevInt
						continue
					}
				}
			}
			break
		} else {
			// Might be a cross-reference stream (PDF 1.5+)
			p.unreadToken(tok)
			objNum, genNum, val, err := d.parseIndirectObjectAt(currOffset)
			if err == nil {
				if stream, ok := val.(pdfStream); ok {
					if typeVal, ok := stream.Dict["Type"].(pdfName); ok && typeVal == "XRef" {
						if err := d.parseXRefStream(stream); err == nil {
							if prev, hasPrev := stream.Dict["Prev"]; hasPrev {
								if prevInt, ok := toInt(prev); ok {
									currOffset = prevInt
									continue
								}
							}
							break
						}
					}
				}
				d.objects[pdfRef{Num: uint32(objNum), Gen: uint16(genNum)}] = val
			}
			break
		}
	}

	if d.root == nil && d.rootRef != nil {
		resolvedRoot, err := d.Resolve(d.rootRef)
		if err == nil {
			if rDict, isDict := resolvedRoot.(pdfDict); isDict {
				d.root = rDict
			}
		}
	}

	if d.root == nil {
		return errors.New("pdfextract: Root catalog not found via xref/trailer")
	}
	return nil
}

// findStartXRef scans backward from the end of the file to find "startxref".
func (d *PDFDoc) findStartXRef() int64 {
	searchLen := 2048
	if len(d.data) < searchLen {
		searchLen = len(d.data)
	}
	tail := d.data[len(d.data)-searchLen:]
	idx := bytes.LastIndex(tail, []byte("startxref"))
	if idx < 0 {
		return -1
	}
	sub := tail[idx+len("startxref"):]
	p := newParser(sub)
	tok, err := p.nextNonSpaceToken()
	if err != nil {
		return -1
	}
	val, err := strconv.ParseInt(tok, 10, 64)
	if err != nil {
		return -1
	}
	return val
}

// parseClassicXRef reads traditional xref sections.
func (d *PDFDoc) parseClassicXRef(p *parser) error {
	for {
		firstTok, err := p.nextNonSpaceToken()
		if err != nil {
			return err
		}
		if firstTok == "trailer" {
			p.unreadToken(firstTok)
			return nil
		}
		countTok, err := p.nextNonSpaceToken()
		if err != nil {
			return err
		}
		first, err := strconv.ParseInt(firstTok, 10, 64)
		if err != nil {
			return err
		}
		count, err := strconv.ParseInt(countTok, 10, 64)
		if err != nil {
			return err
		}

		for i := int64(0); i < count; i++ {
			offsetTok, err := p.nextNonSpaceToken()
			if err != nil {
				return err
			}
			if _, err := p.nextNonSpaceToken(); err != nil {
				return err
			}
			statusTok, err := p.nextNonSpaceToken()
			if err != nil {
				return err
			}
			offset, _ := strconv.ParseInt(offsetTok, 10, 64)
			if statusTok == "n" && offset > 0 && offset < int64(len(d.data)) {
				d.xrefOffsets[uint32(first+i)] = offset
			}
		}
	}
}

// parseXRefStream reads cross-reference streams.
func (d *PDFDoc) parseXRefStream(stream pdfStream) error {
	if rootRef, hasRoot := stream.Dict["Root"]; hasRoot && d.rootRef == nil {
		d.rootRef = rootRef
	}

	wArr, ok := stream.Dict["W"].(pdfArray)
	if !ok || len(wArr) < 3 {
		return errors.New("invalid W in XRef stream")
	}
	w0, _ := toInt(wArr[0])
	w1, _ := toInt(wArr[1])
	w2, _ := toInt(wArr[2])
	entrySize := int(w0 + w1 + w2)
	if entrySize <= 0 {
		return errors.New("invalid entry size in XRef stream")
	}

	data := stream.Data
	if filter, ok := stream.Dict["Filter"].(pdfName); ok && filter == "FlateDecode" {
		decoded, err := decompressZlib(data)
		if err == nil {
			data = decoded
		}
	}

	// Handle Predictor if present
	if parms, ok := stream.Dict["DecodeParms"].(pdfDict); ok {
		if pred, ok := toInt(parms["Predictor"]); ok && pred >= 10 {
			columns := int(entrySize)
			if c, ok := toInt(parms["Columns"]); ok && c > 0 {
				columns = int(c)
			}
			unpredicted, err := undoPNGPrediction(data, columns)
			if err == nil {
				data = unpredicted
			}
		}
	}

	indexArr, ok := stream.Dict["Index"].(pdfArray)
	var intervals [][2]int64
	if ok && len(indexArr) >= 2 {
		for i := 0; i < len(indexArr)-1; i += 2 {
			first, _ := toInt(indexArr[i])
			count, _ := toInt(indexArr[i+1])
			intervals = append(intervals, [2]int64{first, count})
		}
	} else {
		size, _ := toInt(stream.Dict["Size"])
		intervals = append(intervals, [2]int64{0, size})
	}

	buf := bytes.NewReader(data)
	for _, interval := range intervals {
		first := interval[0]
		count := interval[1]
		for i := int64(0); i < count; i++ {
			entry := make([]byte, entrySize)
			if _, err := io.ReadFull(buf, entry); err != nil {
				break
			}
			var f0, f1, f2 int64
			offset := 0
			if w0 > 0 {
				f0 = readBigEndianInt(entry[offset : offset+int(w0)])
				offset += int(w0)
			} else {
				f0 = 1 // default type is 1
			}
			if w1 > 0 {
				f1 = readBigEndianInt(entry[offset : offset+int(w1)])
				offset += int(w1)
			}
			if w2 > 0 {
				f2 = readBigEndianInt(entry[offset : offset+int(w2)])
			}

			objNum := uint32(first + i)
			if f0 == 1 { // uncompressed object
				d.xrefOffsets[objNum] = f1
			} else if f0 == 2 { // object in an object stream
				d.objStmLocs[objNum] = objStmLoc{stmObjNum: uint32(f1), index: int(f2)}
			}
		}
	}
	return nil
}

func readBigEndianInt(b []byte) int64 {
	var val int64
	for _, v := range b {
		val = (val << 8) | int64(v)
	}
	return val
}

// loadObjectAt reads and stores an object at a specific file offset.
func (d *PDFDoc) loadObjectAt(ref pdfRef, offset int64) {
	num, gen, val, err := d.parseIndirectObjectAt(offset)
	if err == nil && uint32(num) == ref.Num {
		d.objects[pdfRef{Num: uint32(num), Gen: uint16(gen)}] = val
	}
}

// parseIndirectObjectAt reads an indirect object starting at file offset.
func (d *PDFDoc) parseIndirectObjectAt(offset int64) (int64, int64, any, error) {
	if offset < 0 || offset >= int64(len(d.data)) {
		return 0, 0, nil, errors.New("offset out of bounds")
	}
	p := newParser(d.data[offset:])
	numTok, err := p.nextNonSpaceToken()
	if err != nil {
		return 0, 0, nil, err
	}
	genTok, err := p.nextNonSpaceToken()
	if err != nil {
		return 0, 0, nil, err
	}
	objTok, err := p.nextNonSpaceToken()
	if err != nil || objTok != "obj" {
		return 0, 0, nil, errors.New("expected obj token")
	}

	num, err := strconv.ParseInt(numTok, 10, 64)
	if err != nil {
		return 0, 0, nil, err
	}
	gen, err := strconv.ParseInt(genTok, 10, 64)
	if err != nil {
		return 0, 0, nil, err
	}

	val, err := p.parseObject()
	if err != nil {
		return num, gen, nil, err
	}

	// Check if object is followed by a stream
	streamTok, err := p.nextNonSpaceToken()
	if err == nil && streamTok == "stream" {
		p.skipStreamWhitespace()
		if dict, ok := val.(pdfDict); ok {
			var length int64
			if lVal, ok := dict["Length"]; ok {
				if lInt, ok := toInt(lVal); ok {
					length = lInt
				} else if lRef, ok := lVal.(pdfRef); ok {
					if refObj, exists := d.objects[lRef]; exists {
						if lInt, ok := toInt(refObj); ok {
							length = lInt
						}
					}
				}
			}

			var streamData []byte
			if length > 0 && int(p.pos)+int(length) <= len(p.data) {
				streamData = make([]byte, length)
				copy(streamData, p.data[p.pos:int(p.pos)+int(length)])
				p.pos += int(length)
			} else {
				// Search for endstream
				idx := bytes.Index(p.data[p.pos:], []byte("endstream"))
				if idx >= 0 {
					streamData = make([]byte, idx)
					copy(streamData, p.data[p.pos:int(p.pos)+idx])
					p.pos += idx
				}
			}
			val = pdfStream{Dict: dict, Data: streamData}
		}
	}

	return num, gen, val, nil
}

// linearScanObjects scans the entire file for "N G obj" patterns as a fallback.
func (d *PDFDoc) linearScanObjects() error {
	data := d.data
	pattern := []byte(" obj")
	idx := 0
	for {
		found := bytes.Index(data[idx:], pattern)
		if found < 0 {
			break
		}
		objPos := idx + found
		// Walk backwards to find objNum and genNum
		start := objPos - 1
		for start >= 0 && (data[start] == ' ' || data[start] == '\t' || data[start] == '\r' || data[start] == '\n') {
			start--
		}
		endGen := start + 1
		for start >= 0 && data[start] >= '0' && data[start] <= '9' {
			start--
		}
		genStr := string(data[start+1 : endGen])
		for start >= 0 && (data[start] == ' ' || data[start] == '\t' || data[start] == '\r' || data[start] == '\n') {
			start--
		}
		endNum := start + 1
		for start >= 0 && data[start] >= '0' && data[start] <= '9' {
			start--
		}
		numStr := string(data[start+1 : endNum])

		num, err1 := strconv.ParseInt(numStr, 10, 64)
		gen, err2 := strconv.ParseInt(genStr, 10, 64)
		if err1 == nil && err2 == nil && num > 0 {
			p := newParser(data[objPos+len(" obj"):])
			val, err := p.parseObject()
			if err == nil {
				// Check for stream
				streamTok, sErr := p.nextNonSpaceToken()
				if sErr == nil && streamTok == "stream" {
					p.skipStreamWhitespace()
					if dict, ok := val.(pdfDict); ok {
						endIdx := bytes.Index(p.data[p.pos:], []byte("endstream"))
						if endIdx >= 0 {
							val = pdfStream{
								Dict: dict,
								Data: p.data[p.pos : int(p.pos)+endIdx],
							}
						}
					}
				}
				ref := pdfRef{Num: uint32(num), Gen: uint16(gen)}
				d.objects[ref] = val
				if dict, ok := val.(pdfDict); ok {
					if t, ok := dict["Type"].(pdfName); ok && t == "Catalog" {
						d.root = dict
					}
				}
			}
		}
		idx = objPos + len(pattern)
	}

	if d.root == nil {
		// Search for any dictionary with /Pages
		for _, obj := range d.objects {
			if dict, ok := obj.(pdfDict); ok {
				if _, hasPages := dict["Pages"]; hasPages {
					d.root = dict
					break
				}
			}
		}
	}

	if d.root == nil {
		return errors.New("pdfextract: Root catalog could not be found via linear scan")
	}
	return nil
}

// Resolve resolves a value, dereferencing pdfRef references.
func (d *PDFDoc) Resolve(val any) (any, error) {
	ref, isRef := val.(pdfRef)
	if !isRef {
		return val, nil
	}
	if obj, exists := d.objects[ref]; exists {
		return obj, nil
	}
	// Try loading from uncompressed file offset
	if offset, hasOffset := d.xrefOffsets[ref.Num]; hasOffset {
		d.loadObjectAt(ref, offset)
		if obj, exists := d.objects[ref]; exists {
			return obj, nil
		}
	}
	// Try loading from object stream
	if loc, hasLoc := d.objStmLocs[ref.Num]; hasLoc {
		if err := d.loadObjectStream(loc.stmObjNum); err == nil {
			if obj, exists := d.objects[ref]; exists {
				return obj, nil
			}
		}
	}
	return nil, fmt.Errorf("pdfextract: unresolved reference %d %d R", ref.Num, ref.Gen)
}

func (d *PDFDoc) loadObjectStream(stmObjNum uint32) error {
	if d.loadedObjStm[stmObjNum] {
		return nil
	}
	d.loadedObjStm[stmObjNum] = true

	stmRef := pdfRef{Num: stmObjNum, Gen: 0}
	stmVal, err := d.Resolve(stmRef)
	if err != nil {
		return err
	}
	stream, ok := stmVal.(pdfStream)
	if !ok {
		return fmt.Errorf("pdfextract: object %d is not a stream", stmObjNum)
	}

	decoded, err := d.decodeStream(stream)
	if err != nil {
		return err
	}

	nVal, _ := toInt(stream.Dict["N"])
	firstVal, _ := toInt(stream.Dict["First"])
	if nVal <= 0 || firstVal <= 0 || int(firstVal) > len(decoded) {
		return errors.New("pdfextract: invalid /N or /First in ObjStm")
	}

	// Read N pairs of (objNum, offset) from the header before 'firstVal'
	pHeader := newParser(decoded[:firstVal])
	type objEntry struct {
		num uint32
		off int
	}
	var entries []objEntry
	for i := int64(0); i < nVal; i++ {
		numTok, err1 := pHeader.nextNonSpaceToken()
		offTok, err2 := pHeader.nextNonSpaceToken()
		if err1 != nil || err2 != nil {
			break
		}
		num, errN := strconv.ParseUint(numTok, 10, 32)
		off, errO := strconv.Atoi(offTok)
		if errN == nil && errO == nil {
			entries = append(entries, objEntry{num: uint32(num), off: off})
		}
	}

	bodyData := decoded[firstVal:]
	for _, entry := range entries {
		if entry.off < 0 || entry.off >= len(bodyData) {
			continue
		}
		pObj := newParser(bodyData[entry.off:])
		val, err := pObj.parseObject()
		if err == nil {
			ref := pdfRef{Num: entry.num, Gen: 0}
			d.objects[ref] = val
		}
	}
	return nil
}

// resolvePages walks the /Pages tree starting from /Root to collect leaf /Page objects.
func (d *PDFDoc) resolvePages() error {
	pagesRef, hasPages := d.root["Pages"]
	if !hasPages {
		return errors.New("pdfextract: catalog missing /Pages")
	}

	pagesObj, err := d.Resolve(pagesRef)
	if err != nil {
		return err
	}
	pagesDict, ok := pagesObj.(pdfDict)
	if !ok {
		return errors.New("pdfextract: /Pages is not a dictionary")
	}

	return d.collectPages(pagesDict)
}

func (d *PDFDoc) collectPages(dict pdfDict) error {
	nodeType, _ := dict["Type"].(pdfName)
	if nodeType == "Page" {
		d.pages = append(d.pages, dict)
		return nil
	}

	kidsVal, ok := dict["Kids"]
	if !ok {
		return nil
	}
	kidsResolved, err := d.Resolve(kidsVal)
	if err != nil {
		return err
	}
	kidsArr, ok := kidsResolved.(pdfArray)
	if !ok {
		return nil
	}

	for _, kid := range kidsArr {
		childObj, err := d.Resolve(kid)
		if err != nil {
			continue
		}
		childDict, ok := childObj.(pdfDict)
		if !ok {
			continue
		}
		if err := d.collectPages(childDict); err != nil {
			return err
		}
	}
	return nil
}

// resolvePageAttribute walks up the /Pages tree through /Parent references
// to find inherited attributes (MediaBox, CropBox, Resources, Rotate).
func (d *PDFDoc) resolvePageAttribute(dict pdfDict, key string) any {
	curr := dict
	visited := make(map[pdfRef]bool)
	for {
		if val, ok := curr[key]; ok {
			if resolved, err := d.Resolve(val); err == nil && resolved != nil {
				return resolved
			}
		}
		parentVal, ok := curr["Parent"]
		if !ok {
			break
		}
		if ref, ok := parentVal.(pdfRef); ok {
			if visited[ref] {
				break
			}
			visited[ref] = true
		}
		parentObj, err := d.Resolve(parentVal)
		if err != nil || parentObj == nil {
			break
		}
		parentDict, ok := parentObj.(pdfDict)
		if !ok {
			break
		}
		curr = parentDict
	}
	return nil
}

// pageRotationMatrix computes the 2D transformation matrix and display bounds for a rotated page.
func pageRotationMatrix(rotate int, mb Rect) (Matrix, Rect) {
	switch rotate {
	case 90:
		// 90 deg clockwise: x' = y - Y0, y' = X1 - x
		rotM := Matrix{0, -1, 1, 0, -mb.Y0, mb.X1}
		newMB := Rect{X0: 0, Y0: 0, X1: mb.Height(), Y1: mb.Width()}
		return rotM, newMB
	case 180:
		rotM := Matrix{-1, 0, 0, -1, mb.X1 + mb.X0, mb.Y1 + mb.Y0}
		return rotM, mb
	case 270:
		// 270 deg clockwise: x' = Y1 - y, y' = x - X0
		rotM := Matrix{0, 1, -1, 0, mb.Y1, -mb.X0}
		newMB := Rect{X0: 0, Y0: 0, X1: mb.Height(), Y1: mb.Width()}
		return rotM, newMB
	default:
		return IdentityMatrix(), mb
	}
}

// ExtractPage parses and extracts all text, lines, rects, and images on page index (0-based).
func (d *PDFDoc) ExtractPage(pageIndex int) (*ParsedPage, error) {
	if pageIndex < 0 || pageIndex >= len(d.pages) {
		return nil, fmt.Errorf("pdfextract: page index %d out of bounds (total %d)", pageIndex, len(d.pages))
	}

	pageDict := d.pages[pageIndex]

	// Determine MediaBox (checking inherited attribute up the /Pages tree)
	mediaBox := Rect{0, 0, 612, 792} // default US Letter
	if mbResolved := d.resolvePageAttribute(pageDict, "MediaBox"); mbResolved != nil {
		if mbArr, ok := mbResolved.(pdfArray); ok && len(mbArr) >= 4 {
			x0, _ := toFloat(mbArr[0])
			y0, _ := toFloat(mbArr[1])
			x1, _ := toFloat(mbArr[2])
			y1, _ := toFloat(mbArr[3])
			mediaBox = Rect{
				X0: math.Min(x0, x1),
				Y0: math.Min(y0, y1),
				X1: math.Max(x0, x1),
				Y1: math.Max(y0, y1),
			}
		}
	}

	// CropBox override if specified
	if cbResolved := d.resolvePageAttribute(pageDict, "CropBox"); cbResolved != nil {
		if cbArr, ok := cbResolved.(pdfArray); ok && len(cbArr) >= 4 {
			x0, _ := toFloat(cbArr[0])
			y0, _ := toFloat(cbArr[1])
			x1, _ := toFloat(cbArr[2])
			y1, _ := toFloat(cbArr[3])
			cropBox := Rect{
				X0: math.Min(x0, x1),
				Y0: math.Min(y0, y1),
				X1: math.Max(x0, x1),
				Y1: math.Max(y0, y1),
			}
			if cropBox.Width() > 0 && cropBox.Height() > 0 {
				mediaBox = cropBox
			}
		}
	}

	// Resolve page rotation (0, 90, 180, 270)
	rotate := 0
	if rotResolved := d.resolvePageAttribute(pageDict, "Rotate"); rotResolved != nil {
		if rotInt, ok := toInt(rotResolved); ok {
			rotate = int((rotInt%360 + 360) % 360)
		}
	}

	// Resolve Resources (inherited)
	resourcesDict := make(pdfDict)
	if resResolved := d.resolvePageAttribute(pageDict, "Resources"); resResolved != nil {
		if rDict, ok := resResolved.(pdfDict); ok {
			resourcesDict = rDict
		}
	}

	// Resolve Fonts in Resources
	fonts := make(map[string]*pdfFont)
	if fontVal, ok := resourcesDict["Font"]; ok {
		if fontResolved, err := d.Resolve(fontVal); err == nil {
			if fDict, ok := fontResolved.(pdfDict); ok {
				for fname, fRef := range fDict {
					if fObj, err := d.Resolve(fRef); err == nil {
						if fontSpec, ok := fObj.(pdfDict); ok {
							fonts[fname] = d.parseFont(fontSpec)
						}
					}
				}
			}
		}
	}

	// Resolve XObjects in Resources
	xobjects := make(map[string]pdfStream)
	if xVal, ok := resourcesDict["XObject"]; ok {
		if xResolved, err := d.Resolve(xVal); err == nil {
			if xDict, ok := xResolved.(pdfDict); ok {
				for xname, xRef := range xDict {
					if xObj, err := d.Resolve(xRef); err == nil {
						if stream, ok := xObj.(pdfStream); ok {
							xobjects[xname] = stream
						}
					}
				}
			}
		}
	}

	// Get Content stream(s)
	var contentBytes []byte
	if cVal, ok := pageDict["Contents"]; ok {
		contentObj, err := d.Resolve(cVal)
		if err == nil {
			switch cv := contentObj.(type) {
			case pdfStream:
				data, err := d.decodeStream(cv)
				if err == nil {
					contentBytes = data
				}
			case pdfArray:
				for _, elem := range cv {
					elemObj, err := d.Resolve(elem)
					if err == nil {
						if elemStream, ok := elemObj.(pdfStream); ok {
							data, err := d.decodeStream(elemStream)
							if err == nil {
								contentBytes = append(contentBytes, data...)
								contentBytes = append(contentBytes, ' ')
							}
						}
					}
				}
			}
		}
	}

	rotMatrix, visualMediaBox := pageRotationMatrix(rotate, mediaBox)

	page := &ParsedPage{
		Index:    pageIndex,
		MediaBox: visualMediaBox,
	}

	if len(contentBytes) > 0 {
		interpreter := newContentInterpreter(contentBytes, fonts, xobjects, visualMediaBox, rotMatrix)
		interpreter.interpret(page)
	}

	return page, nil
}

func (d *PDFDoc) decodeStream(stream pdfStream) ([]byte, error) {
	data := stream.Data
	if filter, ok := stream.Dict["Filter"].(pdfName); ok && filter == "FlateDecode" {
		decoded, err := decompressZlib(data)
		if err == nil {
			data = decoded
		}
	}
	return data, nil
}

// pdfFont holds font metadata and ToUnicode translation.
type pdfFont struct {
	BaseFont  string
	ToUnicode map[uint32]string
}

func (d *PDFDoc) parseFont(dict pdfDict) *pdfFont {
	font := &pdfFont{
		ToUnicode: make(map[uint32]string),
	}
	if base, ok := dict["BaseFont"].(pdfName); ok {
		font.BaseFont = string(base)
	}

	if tuVal, ok := dict["ToUnicode"]; ok {
		if tuObj, err := d.Resolve(tuVal); err == nil {
			if tuStream, ok := tuObj.(pdfStream); ok {
				streamData, err := d.decodeStream(tuStream)
				if err == nil {
					font.parseToUnicodeCMap(streamData)
				}
			}
		}
	}

	// Fallback to /Encoding and /Differences for non-ToUnicode fonts
	if len(font.ToUnicode) == 0 {
		d.applyFontEncoding(font, dict)
	}

	return font
}

var standardAdobeGlyphs = map[string]string{
	"quotesingle":    "'",
	"quotedbl":       "\"",
	"quoteleft":      "‘",
	"quoteright":     "’",
	"quotedblleft":   "“",
	"quotedblright":  "”",
	"quotesinglbase": "‚",
	"quotedblbase":   "„",
	"guilsinglleft":  "‹",
	"guilsinglright": "›",
	"guillemotleft":  "«",
	"guillemotright": "»",
	"bullet":         "•",
	"endash":         "–",
	"emdash":         "—",
	"minus":          "-",
	"hyphen":         "-",
	"periodcentered": "·",
	"dagger":         "†",
	"daggerdbl":      "‡",
	"section":        "§",
	"paragraph":      "¶",
	"ellipsis":       "…",
	"fraction":       "/",
	"copyright":      "©",
	"registered":     "®",
	"trademark":      "™",
	"degree":         "°",
	"plusminus":      "±",
	"multiply":       "×",
	"divide":         "÷",
	"fi":             "fi",
	"fl":             "fl",
	"ff":             "ff",
	"ffi":            "ffi",
	"ffl":            "ffl",
	"cent":           "¢",
	"sterling":       "£",
	"yen":            "¥",
	"euro":           "€",
	"currency":       "¤",
	"onehalf":        "½",
	"onequarter":     "¼",
	"threequarters":  "¾",
	"Delta":          "Δ",
	"Omega":          "Ω",
	"mu":             "μ",
	"pi":             "π",
	"radical":        "√",
	"infinity":       "∞",
	"summation":      "∑",
	"integral":       "∫",
	"notequal":       "≠",
	"lessequal":      "≤",
	"greaterequal":   "≥",
	"approxequal":    "≈",
}

var winAnsiHighBytes = map[byte]rune{
	0x80: '€',
	0x82: '‚',
	0x83: 'ƒ',
	0x84: '„',
	0x85: '…',
	0x86: '†',
	0x87: '‡',
	0x88: 'ˆ',
	0x89: '‰',
	0x8A: 'Š',
	0x8B: '‹',
	0x8C: 'Œ',
	0x8E: 'Ž',
	0x91: '‘',
	0x92: '’',
	0x93: '“',
	0x94: '”',
	0x95: '•',
	0x96: '–',
	0x97: '—',
	0x98: '˜',
	0x99: '™',
	0x9A: 'š',
	0x9B: '›',
	0x9C: 'œ',
	0x9E: 'ž',
	0x9F: 'Ÿ',
}

func adobeGlyphToUnicode(name string) string {
	if r, ok := standardAdobeGlyphs[name]; ok {
		return r
	}
	if strings.HasPrefix(name, "uni") && len(name) == 7 {
		if val, err := strconv.ParseUint(name[3:], 16, 32); err == nil {
			return string(rune(val))
		}
	}
	if strings.HasPrefix(name, "u") && len(name) == 5 {
		if val, err := strconv.ParseUint(name[1:], 16, 32); err == nil {
			return string(rune(val))
		}
	}
	return ""
}

func (d *PDFDoc) applyFontEncoding(f *pdfFont, dict pdfDict) {
	// Baseline: ASCII 0x20..0x7E + WinAnsi / Latin-1 for 0x80..0xFF
	for b := 0x20; b <= 0xFF; b++ {
		byteVal := byte(b)
		if r, ok := winAnsiHighBytes[byteVal]; ok {
			f.ToUnicode[uint32(b)] = string(r)
		} else {
			f.ToUnicode[uint32(b)] = string(rune(b))
		}
	}

	encVal, ok := dict["Encoding"]
	if !ok {
		return
	}
	encObj, err := d.Resolve(encVal)
	if err != nil || encObj == nil {
		return
	}

	if encDict, ok := encObj.(pdfDict); ok {
		if diffVal, ok := encDict["Differences"]; ok {
			diffObj, err := d.Resolve(diffVal)
			if err == nil {
				if diffArr, ok := diffObj.(pdfArray); ok {
					currentCode := uint32(0)
					for _, item := range diffArr {
						if num, ok := toInt(item); ok {
							currentCode = uint32(num)
						} else if gName, ok := item.(pdfName); ok {
							str := adobeGlyphToUnicode(string(gName))
							if str != "" {
								f.ToUnicode[currentCode] = str
							}
							currentCode++
						}
					}
				}
			}
		}
	}
}

func (f *pdfFont) parseToUnicodeCMap(data []byte) {
	dataStr := strings.ReplaceAll(string(data), "\r\n", "\n")
	dataStr = strings.ReplaceAll(dataStr, "\r", "\n")
	lines := strings.Split(dataStr, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasSuffix(line, "beginbfchar") {
			count, _ := strconv.Atoi(strings.Fields(line)[0])
			for j := 0; j < count && i+1 < len(lines); j++ {
				i++
				entry := strings.TrimSpace(lines[i])
				if strings.HasSuffix(entry, "endbfchar") {
					break
				}
				fields := strings.Fields(entry)
				for k := 0; k+1 < len(fields); k += 2 {
					srcHex := strings.Trim(fields[k], "<>")
					dstHex := strings.Trim(fields[k+1], "<>")
					srcCode, err := strconv.ParseUint(srcHex, 16, 32)
					if err == nil {
						f.ToUnicode[uint32(srcCode)] = decodeHexUTF16(dstHex)
					}
				}
			}
		} else if strings.HasSuffix(line, "beginbfrange") {
			count, _ := strconv.Atoi(strings.Fields(line)[0])
			for j := 0; j < count && i+1 < len(lines); j++ {
				i++
				entry := strings.TrimSpace(lines[i])
				if strings.HasSuffix(entry, "endbfrange") {
					break
				}
				fields := strings.Fields(entry)
				if len(fields) >= 3 {
					startHex := strings.Trim(fields[0], "<>")
					endHex := strings.Trim(fields[1], "<>")
					startCode, _ := strconv.ParseUint(startHex, 16, 32)
					endCode, _ := strconv.ParseUint(endHex, 16, 32)

					if strings.HasPrefix(fields[2], "[") {
						// Form 2: <start> <end> [ <dst1> <dst2> ... ]
						var dstTokens []string
						for k := 2; k < len(fields); k++ {
							tok := strings.Trim(fields[k], "[]<>")
							if tok != "" {
								dstTokens = append(dstTokens, tok)
							}
						}
						for !strings.Contains(lines[i], "]") && i+1 < len(lines) {
							i++
							more := strings.Fields(lines[i])
							for _, tok := range more {
								cleaned := strings.Trim(tok, "[]<>")
								if cleaned != "" {
									dstTokens = append(dstTokens, cleaned)
								}
							}
						}
						for idx, tok := range dstTokens {
							code := startCode + uint64(idx)
							if code > endCode {
								break
							}
							f.ToUnicode[uint32(code)] = decodeHexUTF16(tok)
						}
					} else {
						// Form 1: Sequential destination
						dstHex := strings.Trim(fields[2], "<>")
						runes := decodeHexUTF16Runes(dstHex)
						if len(runes) == 1 {
							baseRune := runes[0]
							offset := rune(0)
							for code := startCode; code <= endCode; code++ {
								f.ToUnicode[uint32(code)] = string(baseRune + offset)
								offset++
							}
						} else if len(runes) > 1 {
							offset := rune(0)
							for code := startCode; code <= endCode; code++ {
								copyRunes := make([]rune, len(runes))
								copy(copyRunes, runes)
								copyRunes[len(copyRunes)-1] += offset
								f.ToUnicode[uint32(code)] = string(copyRunes)
								offset++
							}
						} else {
							baseDst, _ := strconv.ParseUint(dstHex, 16, 32)
							offset := uint32(0)
							for code := startCode; code <= endCode; code++ {
								f.ToUnicode[uint32(code)] = string(rune(uint32(baseDst) + offset))
								offset++
							}
						}
					}
				}
			}
		}
	}
}

func decodeHexUTF16(hexStr string) string {
	runes := decodeHexUTF16Runes(hexStr)
	if len(runes) == 0 {
		return ""
	}
	return string(runes)
}

func decodeHexUTF16Runes(hexStr string) []rune {
	b, err := hex.DecodeString(hexStr)
	if err != nil || len(b) == 0 {
		return nil
	}
	if len(b)%2 != 0 {
		r := make([]rune, len(b))
		for i, v := range b {
			r[i] = rune(v)
		}
		return r
	}
	u16 := make([]uint16, len(b)/2)
	for i := 0; i < len(u16); i++ {
		u16[i] = binary.BigEndian.Uint16(b[i*2 : i*2+2])
	}
	return utf16.Decode(u16)
}

func (f *pdfFont) DecodeString(b []byte) string {
	if len(f.ToUnicode) > 0 {
		var out strings.Builder
		// Try 2-byte then 1-byte lookup
		for i := 0; i < len(b); {
			if i+1 < len(b) {
				code2 := (uint32(b[i]) << 8) | uint32(b[i+1])
				if mapped, ok := f.ToUnicode[code2]; ok {
					out.WriteString(mapped)
					i += 2
					continue
				}
			}
			code1 := uint32(b[i])
			if mapped, ok := f.ToUnicode[code1]; ok {
				out.WriteString(mapped)
				i++
				continue
			}
			// Fallback: standard ASCII/Latin
			out.WriteByte(b[i])
			i++
		}
		return out.String()
	}
	// Fallback to ASCII / UTF-8
	if utf8.Valid(b) {
		return string(b)
	}
	// ISO-8859-1 conversion
	runes := make([]rune, len(b))
	for i, c := range b {
		runes[i] = rune(c)
	}
	return string(runes)
}

// contentInterpreter executes content stream operators into page geometries.
type contentInterpreter struct {
	tokens   []string
	fonts    map[string]*pdfFont
	xobjects map[string]pdfStream
	mediaBox Rect

	// Graphics State Stack
	ctmStack []Matrix
	ctm      Matrix

	// Text State
	inText     bool
	tm         Matrix
	tlm        Matrix
	currFont   *pdfFont
	currFontSz float64

	// Path Construction State
	currPath []Point
}

func newContentInterpreter(content []byte, fonts map[string]*pdfFont, xobjects map[string]pdfStream, mediaBox Rect, initialCTM Matrix) *contentInterpreter {
	tokens := tokenizeContentStream(content)
	return &contentInterpreter{
		tokens:     tokens,
		fonts:      fonts,
		xobjects:   xobjects,
		mediaBox:   mediaBox,
		ctmStack:   nil,
		ctm:        initialCTM,
		tm:         IdentityMatrix(),
		tlm:        IdentityMatrix(),
		currFontSz: 12.0,
	}
}

func (ci *contentInterpreter) interpret(page *ParsedPage) {
	var args []string

	for i := 0; i < len(ci.tokens); i++ {
		tok := ci.tokens[i]

		switch tok {
		// Graphics State Operators
		case "q":
			ci.ctmStack = append(ci.ctmStack, ci.ctm)
			args = nil
		case "Q":
			if len(ci.ctmStack) > 0 {
				ci.ctm = ci.ctmStack[len(ci.ctmStack)-1]
				ci.ctmStack = ci.ctmStack[:len(ci.ctmStack)-1]
			}
			args = nil
		case "cm":
			if len(args) >= 6 {
				m := parseMatrixArgs(args[len(args)-6:])
				ci.ctm = m.Multiply(ci.ctm)
			}
			args = nil

		// Text Object Operators
		case "BT":
			ci.inText = true
			ci.tm = IdentityMatrix()
			ci.tlm = IdentityMatrix()
			args = nil
		case "ET":
			ci.inText = false
			args = nil
		case "Tf":
			if len(args) >= 2 {
				fname := strings.TrimPrefix(args[len(args)-2], "/")
				ci.currFont = ci.fonts[fname]
				ci.currFontSz, _ = strconv.ParseFloat(args[len(args)-1], 64)
			}
			args = nil
		case "Tm":
			if len(args) >= 6 {
				ci.tm = parseMatrixArgs(args[len(args)-6:])
				ci.tlm = ci.tm
			}
			args = nil
		case "Td":
			if len(args) >= 2 {
				tx, _ := strconv.ParseFloat(args[len(args)-2], 64)
				ty, _ := strconv.ParseFloat(args[len(args)-1], 64)
				trans := Matrix{1, 0, 0, 1, tx, ty}
				ci.tlm = trans.Multiply(ci.tlm)
				ci.tm = ci.tlm
			}
			args = nil
		case "TD":
			if len(args) >= 2 {
				tx, _ := strconv.ParseFloat(args[len(args)-2], 64)
				ty, _ := strconv.ParseFloat(args[len(args)-1], 64)
				trans := Matrix{1, 0, 0, 1, tx, ty}
				ci.tlm = trans.Multiply(ci.tlm)
				ci.tm = ci.tlm
			}
			args = nil
		case "T*":
			// Move to next line
			trans := Matrix{1, 0, 0, 1, 0, -ci.currFontSz * 1.2}
			ci.tlm = trans.Multiply(ci.tlm)
			ci.tm = ci.tlm
			args = nil
		case "Tj":
			if len(args) >= 1 {
				rawStr := args[len(args)-1]
				ci.emitText(rawStr, page)
			}
			args = nil
		case "TJ":
			if len(args) >= 1 {
				ci.emitTJ(args[len(args)-1], page)
			}
			args = nil
		case "'":
			trans := Matrix{1, 0, 0, 1, 0, -ci.currFontSz * 1.2}
			ci.tlm = trans.Multiply(ci.tlm)
			ci.tm = ci.tlm
			if len(args) >= 1 {
				ci.emitText(args[len(args)-1], page)
			}
			args = nil

		// Path Construction Operators
		case "m":
			if len(args) >= 2 {
				x, _ := strconv.ParseFloat(args[len(args)-2], 64)
				y, _ := strconv.ParseFloat(args[len(args)-1], 64)
				p := ci.ctm.Transform(x, y)
				ci.currPath = append(ci.currPath, p)
			}
			args = nil
		case "l":
			if len(args) >= 2 {
				x, _ := strconv.ParseFloat(args[len(args)-2], 64)
				y, _ := strconv.ParseFloat(args[len(args)-1], 64)
				p := ci.ctm.Transform(x, y)
				if len(ci.currPath) > 0 {
					last := ci.currPath[len(ci.currPath)-1]
					page.Lines = append(page.Lines, VectorLine{Start: last, End: p})
				}
				ci.currPath = append(ci.currPath, p)
			}
			args = nil
		case "re":
			if len(args) >= 4 {
				x, _ := strconv.ParseFloat(args[len(args)-4], 64)
				y, _ := strconv.ParseFloat(args[len(args)-3], 64)
				w, _ := strconv.ParseFloat(args[len(args)-2], 64)
				h, _ := strconv.ParseFloat(args[len(args)-1], 64)
				p0 := ci.ctm.Transform(x, y)
				p1 := ci.ctm.Transform(x+w, y+h)
				r := Rect{
					X0: math.Min(p0.X, p1.X),
					Y0: math.Min(p0.Y, p1.Y),
					X1: math.Max(p0.X, p1.X),
					Y1: math.Max(p0.Y, p1.Y),
				}
				page.Rects = append(page.Rects, VectorRect{BBox: r, Stroke: true})
			}
			args = nil
		case "s", "S":
			ci.currPath = nil
			args = nil
		case "f", "F", "f*", "B", "B*", "b", "b*":
			ci.currPath = nil
			args = nil

		// XObject Invocation
		case "Do":
			if len(args) >= 1 {
				xname := strings.TrimPrefix(args[len(args)-1], "/")
				if stream, ok := ci.xobjects[xname]; ok {
					ci.emitXObject(xname, stream, page)
				}
			}
			args = nil

		default:
			args = append(args, tok)
		}
	}
}

func (ci *contentInterpreter) emitText(raw string, page *ParsedPage) {
	bytesVal := parseStringToken(raw)
	var text string
	if ci.currFont != nil {
		text = ci.currFont.DecodeString(bytesVal)
	} else {
		text = string(bytesVal)
	}
	ci.emitDecodedText(text, page)
}

func (ci *contentInterpreter) emitDecodedText(text string, page *ParsedPage) {
	trimmed := strings.Trim(text, "\r\n")
	if strings.TrimSpace(trimmed) == "" {
		return
	}
	text = trimmed

	// Calculate text position in page coordinates
	// Final matrix = Tm * CTM
	effectiveMatrix := ci.tm.Multiply(ci.ctm)
	pos := effectiveMatrix.Transform(0, 0)
	fontSize := ci.currFontSz * math.Sqrt(math.Abs(effectiveMatrix[0]*effectiveMatrix[3]-effectiveMatrix[1]*effectiveMatrix[2]))
	if fontSize <= 0 {
		fontSize = ci.currFontSz
	}

	fname := ""
	if ci.currFont != nil {
		fname = ci.currFont.BaseFont
	}

	// Estimate approximate string width. Proportional body fonts have average char width ~0.35 * fontSize
	charFactor := 0.35
	lName := strings.ToLower(fname)
	if strings.Contains(lName, "mono") || strings.Contains(lName, "courier") || strings.Contains(lName, "cmtt") {
		charFactor = 0.60
	}
	estWidth := float64(len(text)) * fontSize * charFactor
	estHeight := fontSize

	bbox := Rect{
		X0: pos.X,
		Y0: pos.Y,
		X1: pos.X + estWidth,
		Y1: pos.Y + estHeight,
	}

	page.Spans = append(page.Spans, TextSpan{
		Text:     text,
		FontName: fname,
		FontSize: fontSize,
		BBox:     bbox,
		Matrix:   [6]float64(effectiveMatrix),
	})

	// Advance Tm horizontally
	advance := Matrix{1, 0, 0, 1, estWidth / (effectiveMatrix[0] + 0.0001), 0}
	ci.tm = advance.Multiply(ci.tm)
}

func (ci *contentInterpreter) emitTJ(arrayToken string, page *ParsedPage) {
	// Parse elements inside [ ... ]
	elems := parseTJArray(arrayToken)
	var sb strings.Builder
	for _, elem := range elems {
		if strings.HasPrefix(elem, "(") || strings.HasPrefix(elem, "<") {
			b := parseStringToken(elem)
			if ci.currFont != nil {
				sb.WriteString(ci.currFont.DecodeString(b))
			} else {
				sb.Write(b)
			}
		} else {
			// Numeric displacement in thousandths of a unit of text space.
			// Negative number is subtracted, moving the cursor to the right (space between words).
			if num, err := strconv.ParseFloat(elem, 64); err == nil {
				if num <= -40.0 {
					if sb.Len() > 0 && !strings.HasSuffix(sb.String(), " ") {
						sb.WriteString(" ")
					}
				}
			}
		}
	}
	str := sb.String()
	if strings.TrimSpace(str) != "" {
		ci.emitDecodedText(str, page)
	}
}

func (ci *contentInterpreter) emitXObject(name string, stream pdfStream, page *ParsedPage) {
	subType, _ := stream.Dict["Subtype"].(pdfName)
	if subType != "Image" {
		return
	}

	width, _ := toInt(stream.Dict["Width"])
	height, _ := toInt(stream.Dict["Height"])

	// Determine rendered bounding box on page using CTM
	// An image in PDF occupies unit square [0,0,1,1] in local space
	p0 := ci.ctm.Transform(0, 0)
	p1 := ci.ctm.Transform(1, 0)
	p2 := ci.ctm.Transform(1, 1)
	p3 := ci.ctm.Transform(0, 1)

	minX := math.Min(math.Min(p0.X, p1.X), math.Min(p2.X, p3.X))
	maxX := math.Max(math.Max(p0.X, p1.X), math.Max(p2.X, p3.X))
	minY := math.Min(math.Min(p0.Y, p1.Y), math.Min(p2.Y, p3.Y))
	maxY := math.Max(math.Max(p0.Y, p1.Y), math.Max(p2.Y, p3.Y))

	bbox := Rect{X0: minX, Y0: minY, X1: maxX, Y1: maxY}

	format := "raw"
	data := stream.Data
	filter, _ := stream.Dict["Filter"].(pdfName)
	if filter == "DCTDecode" {
		format = "jpeg"
	} else if filter == "FlateDecode" {
		format = "png"
		if decoded, err := decompressZlib(stream.Data); err == nil {
			data = decoded
		}
	}

	page.Images = append(page.Images, PageImage{
		Name:   name,
		BBox:   bbox,
		Format: format,
		Width:  int(width),
		Height: int(height),
		Data:   data,
	})
}

func parseMatrixArgs(args []string) Matrix {
	var m Matrix
	for i := 0; i < 6 && i < len(args); i++ {
		m[i], _ = strconv.ParseFloat(args[i], 64)
	}
	return m
}

// Tokenizer helpers
type parser struct {
	data []byte
	pos  int
	peek []string
}

func newParser(data []byte) *parser {
	return &parser{data: data}
}

func (p *parser) nextNonSpaceToken() (string, error) {
	if len(p.peek) > 0 {
		tok := p.peek[len(p.peek)-1]
		p.peek = p.peek[:len(p.peek)-1]
		return tok, nil
	}
	p.skipWhitespace()
	if p.pos >= len(p.data) {
		return "", io.EOF
	}

	ch := p.data[p.pos]

	// Delimiters
	if ch == '(' {
		return p.readParenString()
	}
	if ch == '<' {
		if p.pos+1 < len(p.data) && p.data[p.pos+1] == '<' {
			p.pos += 2
			return "<<", nil
		}
		return p.readHexString()
	}
	if ch == '>' {
		if p.pos+1 < len(p.data) && p.data[p.pos+1] == '>' {
			p.pos += 2
			return ">>", nil
		}
		p.pos++
		return ">", nil
	}
	if ch == '[' || ch == ']' {
		p.pos++
		return string(ch), nil
	}
	if ch == '/' {
		return p.readNameToken()
	}

	// Regular token
	start := p.pos
	for p.pos < len(p.data) {
		c := p.data[p.pos]
		if isDelimiter(c) || isWhitespace(c) {
			break
		}
		p.pos++
	}
	if start == p.pos {
		p.pos++
		return string(ch), nil
	}
	return string(p.data[start:p.pos]), nil
}

func (p *parser) unreadToken(tok string) {
	p.peek = append(p.peek, tok)
}

func (p *parser) skipWhitespace() {
	for p.pos < len(p.data) {
		c := p.data[p.pos]
		if c == '%' { // Comment
			for p.pos < len(p.data) && p.data[p.pos] != '\r' && p.data[p.pos] != '\n' {
				p.pos++
			}
			continue
		}
		if !isWhitespace(c) {
			break
		}
		p.pos++
	}
}

func (p *parser) skipStreamWhitespace() {
	if p.pos < len(p.data) && p.data[p.pos] == '\r' {
		p.pos++
	}
	if p.pos < len(p.data) && p.data[p.pos] == '\n' {
		p.pos++
	}
}

func (p *parser) readParenString() (string, error) {
	start := p.pos
	p.pos++ // Skip opening '('
	depth := 1
	for p.pos < len(p.data) && depth > 0 {
		c := p.data[p.pos]
		if c == '\\' {
			p.pos += 2
			continue
		}
		if c == '(' {
			depth++
		} else if c == ')' {
			depth--
		}
		p.pos++
	}
	return string(p.data[start:p.pos]), nil
}

func (p *parser) readHexString() (string, error) {
	start := p.pos
	p.pos++ // skip '<'
	for p.pos < len(p.data) && p.data[p.pos] != '>' {
		p.pos++
	}
	if p.pos < len(p.data) {
		p.pos++ // skip '>'
	}
	return string(p.data[start:p.pos]), nil
}

func (p *parser) readNameToken() (string, error) {
	start := p.pos
	p.pos++ // skip '/'
	for p.pos < len(p.data) {
		c := p.data[p.pos]
		if isDelimiter(c) || isWhitespace(c) {
			break
		}
		p.pos++
	}
	return string(p.data[start:p.pos]), nil
}

func (p *parser) parseObject() (any, error) {
	tok, err := p.nextNonSpaceToken()
	if err != nil {
		return nil, err
	}

	if tok == "<<" {
		dict := make(pdfDict)
		for {
			keyTok, err := p.nextNonSpaceToken()
			if err != nil {
				return nil, err
			}
			if keyTok == ">>" {
				break
			}
			key := strings.TrimPrefix(keyTok, "/")
			val, err := p.parseObject()
			if err != nil {
				return nil, err
			}
			dict[key] = val
		}
		return dict, nil
	}

	if tok == "[" {
		var arr pdfArray
		for {
			elemTok, err := p.nextNonSpaceToken()
			if err != nil {
				return nil, err
			}
			if elemTok == "]" {
				break
			}
			p.unreadToken(elemTok)
			val, err := p.parseObject()
			if err != nil {
				return nil, err
			}
			arr = append(arr, val)
		}
		return arr, nil
	}

	if strings.HasPrefix(tok, "/") {
		return pdfName(strings.TrimPrefix(tok, "/")), nil
	}

	if tok == "true" {
		return pdfBool(true), nil
	}
	if tok == "false" {
		return pdfBool(false), nil
	}
	if tok == "null" {
		return pdfNull{}, nil
	}

	if strings.HasPrefix(tok, "(") || strings.HasPrefix(tok, "<") {
		return pdfStr(parseStringToken(tok)), nil
	}

	// Number or indirect reference
	if intVal, err := strconv.ParseInt(tok, 10, 64); err == nil {
		next1, err1 := p.nextNonSpaceToken()
		if err1 == nil {
			if genVal, errGen := strconv.ParseInt(next1, 10, 64); errGen == nil {
				next2, err2 := p.nextNonSpaceToken()
				if err2 == nil && next2 == "R" {
					return pdfRef{Num: uint32(intVal), Gen: uint16(genVal)}, nil
				}
				p.unreadToken(next2)
			}
			p.unreadToken(next1)
		}
		return pdfInt(intVal), nil
	}

	if fVal, err := strconv.ParseFloat(tok, 64); err == nil {
		return pdfReal(fVal), nil
	}

	return tok, nil
}

func isWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\f' || b == 0
}

func isDelimiter(b byte) bool {
	return b == '(' || b == ')' || b == '<' || b == '>' || b == '[' || b == ']' || b == '{' || b == '}' || b == '/' || b == '%'
}

func toInt(v any) (int64, bool) {
	switch n := v.(type) {
	case pdfInt:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case pdfReal:
		return int64(n), true
	case float64:
		return int64(n), true
	}
	return 0, false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case pdfReal:
		return float64(n), true
	case float64:
		return n, true
	case pdfInt:
		return float64(n), true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	}
	return 0, false
}

func decompressZlib(in []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(in))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func undoPNGPrediction(data []byte, columns int) ([]byte, error) {
	stride := columns + 1
	if len(data)%stride != 0 {
		return data, nil
	}
	rows := len(data) / stride
	out := make([]byte, rows*columns)
	prevRow := make([]byte, columns)

	for r := 0; r < rows; r++ {
		filterType := data[r*stride]
		rowRaw := data[r*stride+1 : (r+1)*stride]
		currRow := make([]byte, columns)

		switch filterType {
		case 0: // None
			copy(currRow, rowRaw)
		case 1: // Sub
			for c := 0; c < columns; c++ {
				var left byte
				if c > 0 {
					left = currRow[c-1]
				}
				currRow[c] = rowRaw[c] + left
			}
		case 2: // Up
			for c := 0; c < columns; c++ {
				currRow[c] = rowRaw[c] + prevRow[c]
			}
		case 3: // Average
			for c := 0; c < columns; c++ {
				var left int
				if c > 0 {
					left = int(currRow[c-1])
				}
				up := int(prevRow[c])
				currRow[c] = rowRaw[c] + byte((left+up)/2)
			}
		case 4: // Paeth
			for c := 0; c < columns; c++ {
				var a, b, cp int
				if c > 0 {
					a = int(currRow[c-1])
					cp = int(prevRow[c-1])
				}
				b = int(prevRow[c])
				p := a + b - cp
				pa := int(math.Abs(float64(p - a)))
				pb := int(math.Abs(float64(p - b)))
				pc := int(math.Abs(float64(p - cp)))
				var pr byte
				if pa <= pb && pa <= pc {
					pr = byte(a)
				} else if pb <= pc {
					pr = byte(b)
				} else {
					pr = byte(cp)
				}
				currRow[c] = rowRaw[c] + pr
			}
		default:
			copy(currRow, rowRaw)
		}
		copy(out[r*columns:], currRow)
		copy(prevRow, currRow)
	}
	return out, nil
}

func parseStringToken(tok string) []byte {
	if strings.HasPrefix(tok, "(") && strings.HasSuffix(tok, ")") {
		content := tok[1 : len(tok)-1]
		var out bytes.Buffer
		for i := 0; i < len(content); i++ {
			c := content[i]
			if c == '\\' && i+1 < len(content) {
				i++
				esc := content[i]
				switch esc {
				case 'n':
					out.WriteByte('\n')
				case 'r':
					out.WriteByte('\r')
				case 't':
					out.WriteByte('\t')
				case 'b':
					out.WriteByte('\b')
				case 'f':
					out.WriteByte('\f')
				case '\\', '(', ')':
					out.WriteByte(esc)
				default:
					if esc >= '0' && esc <= '7' {
						// Octal
						oct := string(esc)
						for k := 0; k < 2 && i+1 < len(content) && content[i+1] >= '0' && content[i+1] <= '7'; k++ {
							i++
							oct += string(content[i])
						}
						val, _ := strconv.ParseUint(oct, 8, 8)
						out.WriteByte(byte(val))
					} else {
						out.WriteByte(esc)
					}
				}
			} else {
				out.WriteByte(c)
			}
		}
		return out.Bytes()
	}

	if strings.HasPrefix(tok, "<") && strings.HasSuffix(tok, ">") {
		clean := strings.TrimSpace(tok[1 : len(tok)-1])
		if len(clean)%2 != 0 {
			clean += "0"
		}
		b, err := hex.DecodeString(clean)
		if err == nil {
			return b
		}
	}
	return []byte(tok)
}

func tokenizeContentStream(content []byte) []string {
	var tokens []string
	p := newParser(content)
	for {
		p.skipWhitespace()
		if p.pos >= len(p.data) {
			break
		}
		ch := p.data[p.pos]
		if ch == '[' {
			// Read entire array token
			start := p.pos
			p.pos++
			depth := 1
			for p.pos < len(p.data) && depth > 0 {
				c := p.data[p.pos]
				if c == '(' {
					p.readParenString()
					continue
				}
				if c == '<' {
					p.readHexString()
					continue
				}
				if c == '[' {
					depth++
				} else if c == ']' {
					depth--
				}
				p.pos++
			}
			tokens = append(tokens, string(p.data[start:p.pos]))
			continue
		}
		tok, err := p.nextNonSpaceToken()
		if err != nil {
			break
		}
		tokens = append(tokens, tok)
	}
	return tokens
}

func parseTJArray(tok string) []string {
	clean := strings.TrimSpace(tok)
	if strings.HasPrefix(clean, "[") && strings.HasSuffix(clean, "]") {
		clean = clean[1 : len(clean)-1]
	}
	p := newParser([]byte(clean))
	var out []string
	for {
		t, err := p.nextNonSpaceToken()
		if err != nil {
			break
		}
		out = append(out, t)
	}
	return out
}
