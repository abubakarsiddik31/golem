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
	ID      string
	Page    int
	Caption string
	Format  string // "jpeg" or "png"
	Path    string
	Data    []byte
	Width   int
	Height  int
	X0      float64
	Y0      float64
	X1      float64
	Y1      float64
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

	for imgIdx, img := range page.Images {
		// 1. Find adjacent caption text
		caption, matchedSpanIdx := findAdjacentCaption(img, page.Spans, consumedSpanIndices)
		if matchedSpanIdx >= 0 {
			consumedSpanIndices[matchedSpanIdx] = true
		}
		if caption == "" {
			caption = fmt.Sprintf("Figure on page %d (%dx%d)", pageNum+1, img.Width, img.Height)
		}

		// 2. Prepare image data (convert raw pixel streams to PNG if not already JPEG)
		format, imgBytes := prepareImageData(img)

		// 3. Save to disk if imageDir is configured
		relPath := fmt.Sprintf("images/page_%d_img_%d.%s", pageNum+1, imgIdx+1, format)
		if imageDir != "" {
			_ = os.MkdirAll(imageDir, 0755)
			diskPath := filepath.Join(imageDir, fmt.Sprintf("page_%d_img_%d.%s", pageNum+1, imgIdx+1, format))
			if len(imgBytes) > 0 {
				_ = os.WriteFile(diskPath, imgBytes, 0644)
			}
			relPath = diskPath
		}

		imageRefs = append(imageRefs, ImageRef{
			ID:      fmt.Sprintf("img_%d_%d", pageNum+1, imgIdx+1),
			Page:    pageNum,
			Caption: caption,
			Format:  format,
			Path:    relPath,
			Data:    imgBytes,
			Width:   img.Width,
			Height:  img.Height,
			X0:      img.BBox.X0,
			Y0:      img.BBox.Y0,
			X1:      img.BBox.X1,
			Y1:      img.BBox.Y1,
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
	if img.Format == "jpeg" && len(img.Data) > 0 {
		return "jpeg", img.Data
	}

	if len(img.Data) == 0 || img.Width <= 0 || img.Height <= 0 {
		return "png", nil
	}

	// If raw decompressed pixel samples, convert to PNG
	expectedRGB := img.Width * img.Height * 3
	expectedGray := img.Width * img.Height

	var m image.Image
	if len(img.Data) >= expectedRGB {
		rgba := image.NewRGBA(image.Rect(0, 0, img.Width, img.Height))
		idx := 0
		for y := 0; y < img.Height; y++ {
			for x := 0; x < img.Width; x++ {
				r := img.Data[idx]
				g := img.Data[idx+1]
				b := img.Data[idx+2]
				rgba.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
				idx += 3
			}
		}
		m = rgba
	} else if len(img.Data) >= expectedGray {
		gray := image.NewGray(image.Rect(0, 0, img.Width, img.Height))
		idx := 0
		for y := 0; y < img.Height; y++ {
			for x := 0; x < img.Width; x++ {
				gray.SetGray(x, y, color.Gray{Y: img.Data[idx]})
				idx++
			}
		}
		m = gray
	} else {
		// Return raw data as-is
		return "png", img.Data
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err == nil {
		return "png", buf.Bytes()
	}

	return "png", img.Data
}

// BuildImageParts converts extracted images into model.Part slice per Golem ADR 0025.
func BuildImageParts(images []ImageRef) []model.Part {
	var parts []model.Part
	for _, img := range images {
		if len(img.Data) == 0 {
			continue
		}
		mediaType := "image/png"
		if img.Format == "jpeg" {
			mediaType = "image/jpeg"
		}
		part := model.ImageData(mediaType, img.Data)
		if err := part.Validate(); err == nil {
			parts = append(parts, part)
		}
	}
	return parts
}
