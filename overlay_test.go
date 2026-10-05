package govnc

import (
	"image"
	"image/color"
	"testing"

	"github.com/anchorbrowser/govnc/rfb"
)

func TestEncodeAtPlacement(t *testing.T) {
	pf := rfb.DefaultPixelFormat()
	fb := image.Rect(0, 0, 100, 100)

	// Image fully inside framebuffer.
	img := image.NewRGBA(image.Rect(10, 10, 30, 20))
	r, ok, err := encodeAt(img, fb, pf, rfb.EncodingRaw)
	noErr(t, err)
	eq(t, true, ok)
	eq(t, uint16(10), r.X)
	eq(t, uint16(10), r.Y)
	eq(t, uint16(20), r.Width)
	eq(t, uint16(10), r.Height)
}

func TestEncodeAtClipping(t *testing.T) {
	pf := rfb.DefaultPixelFormat()
	fb := image.Rect(0, 0, 100, 100)

	// Image partially outside framebuffer.
	img := image.NewRGBA(image.Rect(90, 90, 110, 110))
	r, ok, err := encodeAt(img, fb, pf, rfb.EncodingRaw)
	noErr(t, err)
	eq(t, true, ok)
	eq(t, uint16(90), r.X)
	eq(t, uint16(90), r.Y)
	eq(t, uint16(10), r.Width)
	eq(t, uint16(10), r.Height)
}

func TestEncodeAtOutsideFramebuffer(t *testing.T) {
	pf := rfb.DefaultPixelFormat()
	fb := image.Rect(0, 0, 100, 100)

	// Image completely outside framebuffer.
	img := image.NewRGBA(image.Rect(200, 200, 210, 210))
	r, ok, err := encodeAt(img, fb, pf, rfb.EncodingRaw)
	noErr(t, err)
	eq(t, false, ok)
	eq(t, uint16(0), r.X)
	eq(t, uint16(0), r.Y)
}

func TestEncodeAtRawEncoding(t *testing.T) {
	pf := rfb.DefaultPixelFormat()
	fb := image.Rect(0, 0, 100, 100)

	img := solid(10, 10, color.RGBA{R: 255, A: 255})
	r, ok, err := encodeAt(img, fb, pf, rfb.EncodingRaw)
	noErr(t, err)
	eq(t, true, ok)
	eq(t, rfb.EncodingRaw, r.Encoding)
	// Raw encoding has width * height * bpp/8 bytes.
	if len(r.Data) == 0 {
		t.Fatal("raw rect data should not be empty")
	}
}

func TestEncodeAtRREEncoding(t *testing.T) {
	pf := rfb.DefaultPixelFormat()
	fb := image.Rect(0, 0, 100, 100)

	img := solid(10, 10, color.RGBA{R: 255, G: 100, B: 50, A: 255})
	r, ok, err := encodeAt(img, fb, pf, rfb.EncodingRRE)
	noErr(t, err)
	eq(t, true, ok)
	eq(t, rfb.EncodingRRE, r.Encoding)
	// RRE for solid color should be compact (header + background, zero subrects).
	if len(r.Data) > 20 {
		t.Fatalf("RRE for solid color should be small, got %d bytes", len(r.Data))
	}
}

func TestEncodeAtZeroSizeFramebuffer(t *testing.T) {
	pf := rfb.DefaultPixelFormat()
	fb := image.Rect(0, 0, 0, 0)

	img := solid(10, 10, color.RGBA{A: 255})
	_, ok, err := encodeAt(img, fb, pf, rfb.EncodingRaw)
	noErr(t, err)
	eq(t, false, ok)
}

func TestEncodeAtNegativeBounds(t *testing.T) {
	pf := rfb.DefaultPixelFormat()
	fb := image.Rect(0, 0, 100, 100)

	// Image with negative starting coordinates but positive size.
	img := image.NewRGBA(image.Rect(-5, -5, 5, 5))
	r, ok, err := encodeAt(img, fb, pf, rfb.EncodingRaw)
	noErr(t, err)
	eq(t, true, ok)
	// Should be clipped to start at (0, 0).
	eq(t, uint16(0), r.X)
	eq(t, uint16(0), r.Y)
	eq(t, uint16(5), r.Width)
	eq(t, uint16(5), r.Height)
}

func TestEncodeAtTouchingEdge(t *testing.T) {
	pf := rfb.DefaultPixelFormat()
	fb := image.Rect(0, 0, 100, 100)

	// Image exactly at the edge.
	img := image.NewRGBA(image.Rect(95, 95, 100, 100))
	r, ok, err := encodeAt(img, fb, pf, rfb.EncodingRaw)
	noErr(t, err)
	eq(t, true, ok)
	eq(t, uint16(95), r.X)
	eq(t, uint16(95), r.Y)
	eq(t, uint16(5), r.Width)
	eq(t, uint16(5), r.Height)
}
