package pdfextract

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"testing"
)

// decodePNG decodes the bytes prepareImageData produced so tests assert on
// pixels, not on the encoder's output bytes.
func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.Decode failed: %v", err)
	}
	return img
}

func rgbAt(t *testing.T, img image.Image, x, y int) (uint8, uint8, uint8) {
	t.Helper()
	r, g, b, _ := img.At(x, y).RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)
}

func TestPrepareImageDataRGB(t *testing.T) {
	img := PageImage{
		Format:           "raw",
		Width:            2,
		Height:           1,
		Data:             []byte{255, 0, 0, 0, 128, 255},
		ColorSpace:       "DeviceRGB",
		BitsPerComponent: 8,
	}
	format, data := prepareImageData(img)
	if format != "png" {
		t.Fatalf("format = %q, want png", format)
	}
	decoded := decodePNG(t, data)
	if r, g, b := rgbAt(t, decoded, 0, 0); r != 255 || g != 0 || b != 0 {
		t.Errorf("pixel 0 = (%d,%d,%d), want (255,0,0)", r, g, b)
	}
	if r, g, b := rgbAt(t, decoded, 1, 0); r != 0 || g != 128 || b != 255 {
		t.Errorf("pixel 1 = (%d,%d,%d), want (0,128,255)", r, g, b)
	}
}

func TestPrepareImageDataGray8(t *testing.T) {
	img := PageImage{
		Format:           "raw",
		Width:            2,
		Height:           1,
		Data:             []byte{0, 200},
		ColorSpace:       "DeviceGray",
		BitsPerComponent: 8,
	}
	format, data := prepareImageData(img)
	if format != "png" {
		t.Fatalf("format = %q, want png", format)
	}
	if r, _, _ := rgbAt(t, decodePNG(t, data), 1, 0); r != 200 {
		t.Errorf("gray pixel = %d, want 200", r)
	}
}

// TestPrepareImageDataCMYK guards the conversion that the old length-based
// heuristic silently mis-rendered as RGB.
func TestPrepareImageDataCMYK(t *testing.T) {
	img := PageImage{
		Format:           "raw",
		Width:            2,
		Height:           1,
		Data:             []byte{0, 0, 0, 0, 0, 0, 0, 255}, // white, black
		ColorSpace:       "DeviceCMYK",
		BitsPerComponent: 8,
	}
	format, data := prepareImageData(img)
	if format != "png" {
		t.Fatalf("format = %q, want png", format)
	}
	decoded := decodePNG(t, data)
	if r, g, b := rgbAt(t, decoded, 0, 0); r != 255 || g != 255 || b != 255 {
		t.Errorf("CMYK 0,0,0,0 = (%d,%d,%d), want white", r, g, b)
	}
	if r, g, b := rgbAt(t, decoded, 1, 0); r != 0 || g != 0 || b != 0 {
		t.Errorf("CMYK 0,0,0,255 = (%d,%d,%d), want black", r, g, b)
	}
}

func TestPrepareImageDataGray1Bit(t *testing.T) {
	// Two pixels: bit 1 (white) then bit 0 (black), packed MSB-first.
	img := PageImage{
		Format:           "raw",
		Width:            2,
		Height:           1,
		Data:             []byte{0b10000000},
		ColorSpace:       "DeviceGray",
		BitsPerComponent: 1,
	}
	format, data := prepareImageData(img)
	if format != "png" {
		t.Fatalf("format = %q, want png", format)
	}
	decoded := decodePNG(t, data)
	if r, _, _ := rgbAt(t, decoded, 0, 0); r != 255 {
		t.Errorf("first bit = %d, want 255", r)
	}
	if r, _, _ := rgbAt(t, decoded, 1, 0); r != 0 {
		t.Errorf("second bit = %d, want 0", r)
	}
}

// TestPrepareImageDataUnknownLayoutIsRaw proves an unrecognised sample layout
// is labelled raw instead of being advertised as a PNG.
func TestPrepareImageDataUnknownLayoutIsRaw(t *testing.T) {
	img := PageImage{
		Format:           "raw",
		Width:            10,
		Height:           10,
		Data:             []byte{1, 2, 3},
		ColorSpace:       "DeviceN",
		BitsPerComponent: 16,
	}
	format, data := prepareImageData(img)
	if format != "raw" {
		t.Fatalf("format = %q, want raw", format)
	}
	if !bytes.Equal(data, img.Data) {
		t.Errorf("data = %v, want the original samples", data)
	}
}

func TestPrepareImageDataPassesThroughEncoded(t *testing.T) {
	jpeg := PageImage{Format: "jpeg", Data: []byte{0xff, 0xd8, 0xff}}
	if format, data := prepareImageData(jpeg); format != "jpeg" || !bytes.Equal(data, jpeg.Data) {
		t.Fatalf("jpeg passthrough = (%q, %v), want (jpeg, original bytes)", format, data)
	}
}

func TestBuildImagePartsSkipsUnsupportedFormats(t *testing.T) {
	parts := BuildImageParts([]ImageRef{
		{Format: "raw", Data: []byte{1, 2, 3}},
		{Format: "jpx", Data: []byte{1, 2, 3}},
		{Format: "png", Data: []byte{1, 2, 3}},
	})
	if len(parts) != 1 {
		t.Fatalf("parts = %d, want 1 (raw and jpx are skipped)", len(parts))
	}
	if parts[0].MediaType != "image/png" {
		t.Errorf("media type = %q, want image/png", parts[0].MediaType)
	}
}

// TestProcessPageImagesReportsWriteFailure proves a failed disk write is
// recorded instead of being silently treated as a saved image.
func TestProcessPageImagesReportsWriteFailure(t *testing.T) {
	dir := t.TempDir()
	// A regular file where the image directory should be makes MkdirAll fail.
	blocker := dir + "/not-a-dir"
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	page := &ParsedPage{
		Index:    0,
		MediaBox: Rect{X0: 0, Y0: 0, X1: 612, Y1: 792},
		Images: []PageImage{{
			Name:             "Im1",
			BBox:             Rect{X0: 0, Y0: 0, X1: 612, Y1: 792},
			Format:           "jpeg",
			Width:            10,
			Height:           10,
			Data:             []byte{0xff, 0xd8, 0xff},
			ColorSpace:       "DeviceRGB",
			BitsPerComponent: 8,
		}},
	}

	refs, _ := ProcessPageImages(page, blocker+"/images", 0)
	if len(refs) != 1 {
		t.Fatalf("refs = %d, want 1", len(refs))
	}
	if refs[0].Err == nil {
		t.Fatal("Err = nil, want the disk-write failure recorded")
	}
	if len(refs[0].Data) == 0 {
		t.Error("Data is empty: the in-memory fallback should be kept")
	}
}
