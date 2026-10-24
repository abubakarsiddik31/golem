package pdfextract

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/abubakarsiddik31/golem/model"
)

// ImageRef represents an extracted image and its detected caption.
type ImageRef struct {
	ID         string
	Page       int
	Caption    string
	Format     string // "jpeg" or "png"
	Path       string
	Data       []byte
	Width      int
	Height     int
	X0         float64
	Y0         float64
	X1         float64
	Y1         float64
	IsPageScan bool // true if this image represents a full-page scanned background
}

var captionRegex = regexp.MustCompile(`(?i)^(figure|fig\.|illustration|chart|graph|map|photo|image|diagram)\s*([0-9a-z.-]+)?\s*[:.-]?\s*(.*)`)

// ProcessPageImages matches images with adjacent caption text, extracts raw image data,
// optionally writes images to disk, and filters out matched caption spans from body text.
func ProcessPageImages(page *ParsedPage, imageDir string, pageNum int) ([]ImageRef, []TextSpan) {
	if len(page.Images) == 0 {
		return nil, page.Spans
	}

	var imageRefs []ImageRef
	consumedSpanIndices := make(map[int]bool)

	isScan := IsScannedPage(page)
	pageArea := page.MediaBox.Area()
	if pageArea <= 0 {
		pageArea = 612 * 792
	}

	var totalImageArea float64
	for _, img := range page.Images {
		totalImageArea += img.BBox.Area()
	}
	pageCoveredByImages := (totalImageArea/pageArea) >= 0.30 || (len(page.Spans) == 0 && len(page.Images) > 0)

	for imgIdx, img := range page.Images {
		isPageScan := false
		if isScan && pageCoveredByImages {
			isPageScan = true
		}

		var caption string
		if isPageScan {
			caption = fmt.Sprintf("Scanned page %d (%dx%d)", pageNum+1, img.Width, img.Height)
		} else {
			// 1. Find adjacent caption text
			var matchedSpanIdx int
			caption, matchedSpanIdx = findAdjacentCaption(img, page.Spans, consumedSpanIndices)
			if matchedSpanIdx >= 0 {
				consumedSpanIndices[matchedSpanIdx] = true
			}
			if caption == "" {
				caption = fmt.Sprintf("Figure on page %d (%dx%d)", pageNum+1, img.Width, img.Height)
			}
		}

		// 2. Prepare image data (convert raw pixel streams to PNG if not already JPEG)
		format, imgBytes := prepareImageData(img)

		// 3. Save to disk if imageDir is configured
		var baseName string
		if isPageScan {
			baseName = fmt.Sprintf("page_%d_scan.%s", pageNum+1, format)
		} else {
			baseName = fmt.Sprintf("page_%d_img_%d.%s", pageNum+1, imgIdx+1, format)
		}

		relPath := filepath.ToSlash(filepath.Join("images", baseName))
		if imageDir != "" {
			_ = os.MkdirAll(imageDir, 0755)
			diskPath := filepath.Join(imageDir, baseName)
			if len(imgBytes) > 0 {
				_ = os.WriteFile(diskPath, imgBytes, 0644)
			}
			relPath = diskPath
		}

		imageRefs = append(imageRefs, ImageRef{
			ID:         fmt.Sprintf("img_%d_%d", pageNum+1, imgIdx+1),
			Page:       pageNum,
			Caption:    caption,
			Format:     format,
			Path:       relPath,
			Data:       imgBytes,
			Width:      img.Width,
			Height:     img.Height,
			X0:         img.BBox.X0,
			Y0:         img.BBox.Y0,
			X1:         img.BBox.X1,
			Y1:         img.BBox.Y1,
			IsPageScan: isPageScan,
		})
	}

	// Filter out consumed caption spans so they aren't repeated in paragraph text
	var remainingSpans []TextSpan
	for idx, s := range page.Spans {
		if !consumedSpanIndices[idx] {
			remainingSpans = append(remainingSpans, s)
		}
	}

	return imageRefs, remainingSpans
}

// findAdjacentCaption locates text directly below or above an image matching caption patterns.
func findAdjacentCaption(img PageImage, spans []TextSpan, alreadyConsumed map[int]bool) (string, int) {
	type candidate struct {
		index int
		text  string
		dist  float64
		isTop bool
	}

	var candidates []candidate

	// Search window:
	// Below image: Y between img.Y0 - 45 and img.Y0
	// Above image: Y between img.Y1 and img.Y1 + 45
	// X overlap: horizontally within img bounds +/- 25pt
	xMin := img.BBox.X0 - 25.0
	xMax := img.BBox.X1 + 25.0

	for i, s := range spans {
		if alreadyConsumed[i] {
			continue
		}

		spanMidX := (s.BBox.X0 + s.BBox.X1) / 2
		if spanMidX < xMin || spanMidX > xMax {
			continue
		}

		// Check below image (in PDF coords, lower Y is below)
		if s.BBox.Y1 <= img.BBox.Y0+5.0 && s.BBox.Y0 >= img.BBox.Y0-45.0 {
			dist := img.BBox.Y0 - s.BBox.Y1
			candidates = append(candidates, candidate{index: i, text: s.Text, dist: dist, isTop: false})
		} else if s.BBox.Y0 >= img.BBox.Y1-5.0 && s.BBox.Y1 <= img.BBox.Y1+45.0 {
			// Check above image
			dist := s.BBox.Y0 - img.BBox.Y1
			candidates = append(candidates, candidate{index: i, text: s.Text, dist: dist, isTop: true})
		}
	}

	if len(candidates) == 0 {
		return "", -1
	}

	// Sort candidates by closest vertical distance
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].dist < candidates[j].dist
	})

	// Priority 1: Candidate matching explicit caption regex
	for _, c := range candidates {
		if captionRegex.MatchString(c.text) {
			return strings.TrimSpace(c.text), c.index
		}
	}

	// Priority 2: Closest candidate below the image if it is a short descriptive line (< 80 chars)
	for _, c := range candidates {
		if !c.isTop && len(c.text) <= 80 && c.dist <= 25.0 {
			return strings.TrimSpace(c.text), c.index
		}
	}

	return "", -1
}

// prepareImageData formats image bytes into valid JPEG or PNG.
func prepareImageData(img PageImage) (string, []byte) {
	// Already-encoded formats pass through unchanged.
	switch img.Format {
	case "jpeg":
		return "jpeg", img.Data
	case "jpx":
		return "jpx", img.Data
	case "ccitt":
		return "ccitt", img.Data
	}

	if len(img.Data) == 0 || img.Width <= 0 || img.Height <= 0 {
		return "png", nil
	}

	pixels := img.Width * img.Height
	bpc := img.BitsPerComponent
	if bpc == 0 {
		bpc = 8
	}

	// Convert explicit color spaces and depths without guessing from the
	// buffer length, which silently mis-renders CMYK and bilevel scans.
	if bpc == 8 {
		switch img.ColorSpace {
		case "DeviceGray", "CalGray", "G":
			if len(img.Data) >= pixels {
				return encodePNG(grayImage(img.Data, img.Width, img.Height), img.Data)
			}
		case "DeviceRGB", "CalRGB", "RGB":
			if len(img.Data) >= pixels*3 {
				return encodePNG(rgbImage(img.Data, img.Width, img.Height), img.Data)
			}
		case "DeviceCMYK", "CMYK":
			if len(img.Data) >= pixels*4 {
				return encodePNG(cmykImage(img.Data, img.Width, img.Height), img.Data)
			}
		}
	} else if bpc == 1 && (img.ColorSpace == "DeviceGray" || img.ColorSpace == "G" || img.ColorSpace == "") {
		rowBytes := (img.Width + 7) / 8
		if len(img.Data) >= rowBytes*img.Height {
			return encodePNG(gray1BitImage(img.Data, img.Width, img.Height, rowBytes), img.Data)
		}
	}

	// Unknown colorspace or depth: keep the size-based heuristic for
	// compatibility. If neither layout fits, return the bytes as "raw"
	// rather than mislabelling them as a PNG.
	if len(img.Data) >= pixels*3 {
		return encodePNG(rgbImage(img.Data, img.Width, img.Height), img.Data)
	}
	if len(img.Data) >= pixels {
		return encodePNG(grayImage(img.Data, img.Width, img.Height), img.Data)
	}
	return "raw", img.Data
}

func encodePNG(m image.Image, fallback []byte) (string, []byte) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		return "raw", fallback
	}
	return "png", buf.Bytes()
}

func rgbImage(data []byte, width, height int) image.Image {
	rgba := image.NewRGBA(image.Rect(0, 0, width, height))
	idx := 0
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			rgba.SetRGBA(x, y, color.RGBA{R: data[idx], G: data[idx+1], B: data[idx+2], A: 255})
			idx += 3
		}
	}
	return rgba
}

func grayImage(data []byte, width, height int) image.Image {
	gray := image.NewGray(image.Rect(0, 0, width, height))
	idx := 0
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			gray.SetGray(x, y, color.Gray{Y: data[idx]})
			idx++
		}
	}
	return gray
}

func cmykImage(data []byte, width, height int) image.Image {
	rgba := image.NewRGBA(image.Rect(0, 0, width, height))
	idx := 0
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			c, m, yel, k := data[idx], data[idx+1], data[idx+2], data[idx+3]
			rgba.SetRGBA(x, y, color.RGBA{
				R: uint8((uint16(c) * uint16(255-k)) / 255),
				G: uint8((uint16(m) * uint16(255-k)) / 255),
				B: uint8((uint16(yel) * uint16(255-k)) / 255),
				A: 255,
			})
			idx += 4
		}
	}
	return rgba
}

func gray1BitImage(data []byte, width, height, rowBytes int) image.Image {
	gray := image.NewGray(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		row := data[y*rowBytes:]
		for x := 0; x < width; x++ {
			bit := (row[x/8] >> uint(7-x%8)) & 1
			v := uint8(0)
			if bit == 1 {
				v = 255
			}
			gray.SetGray(x, y, color.Gray{Y: v})
		}
	}
	return gray
}

// BuildImageParts converts extracted images into model.Part slice per Golem ADR 0025.
func BuildImageParts(images []ImageRef) []model.Part {
	var parts []model.Part
	for _, img := range images {
		if len(img.Data) == 0 {
			continue
		}
		var mediaType string
		switch img.Format {
		case "jpeg":
			mediaType = "image/jpeg"
		case "png":
			mediaType = "image/png"
		default:
			// Raw samples and codecs the model APIs do not accept are skipped
			// rather than mislabelled as PNG.
			continue
		}
		part := model.ImageData(mediaType, img.Data)
		if err := part.Validate(); err == nil {
			parts = append(parts, part)
		}
	}
	return parts
}
