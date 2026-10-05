package rfb

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"testing"
)

// testArrowRows is a 12x19 arrow, hotspot at its tip: 'B' black, 'W' white, '.' transparent.
var testArrowRows = [...]string{
	"W...........",
	"WW..........",
	"WBW.........",
	"WBBW........",
	"WBBBW.......",
	"WBBBBW......",
	"WBBBBBW.....",
	"WBBBBBBW....",
	"WBBBBBBBW...",
	"WBBBBBBBBW..",
	"WBBBBBBBBBW.",
	"WBBBBBBWWWWW",
	"WBBBWBBW....",
	"WBBWWBBW....",
	"WBW..WBBW...",
	"WW...WBBW...",
	"W.....WBBW..",
	"......WBBW..",
	".......WW...",
}

func testArrow() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, len(testArrowRows[0]), len(testArrowRows)))
	for y, row := range testArrowRows {
		for x, c := range row {
			switch c {
			case 'B':
				img.SetRGBA(x, y, color.RGBA{A: 0xff})
			case 'W':
				img.SetRGBA(x, y, color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
			}
		}
	}
	return img
}

func TestCursorRectBytes32bpp(t *testing.T) {
	rect, err := CursorRect(testArrow(), 0, 0, DefaultPixelFormat())
	if err != nil {
		t.Fatal(err)
	}
	if rect.Encoding != EncodingCursor || rect.X != 0 || rect.Y != 0 || rect.Width != 12 || rect.Height != 19 {
		t.Fatalf("rect header %+v", rect)
	}
	if len(rect.Prefix) != 12*19*4 {
		t.Fatalf("pixels %d bytes", len(rect.Prefix))
	}
	// Default format is little-endian BGRX: white is ff ff ff 00, black is 00 00 00 00.
	if !bytes.Equal(rect.Prefix[:4], []byte{0xff, 0xff, 0xff, 0}) {
		t.Fatalf("tip pixel % x", rect.Prefix[:4])
	}
	fill := (2*12 + 1) * 4
	if !bytes.Equal(rect.Prefix[fill:fill+4], []byte{0, 0, 0, 0}) {
		t.Fatalf("fill pixel % x", rect.Prefix[fill:fill+4])
	}
	// Mask rows are (12+7)/8 = 2 bytes.
	if len(rect.Data) != 2*19 {
		t.Fatalf("mask %d bytes", len(rect.Data))
	}
	for _, tt := range []struct {
		row  int
		want []byte
	}{
		{0, []byte{0x80, 0x00}},  // W...........
		{10, []byte{0xff, 0xe0}}, // WBBBBBBBBBW.
		{11, []byte{0xff, 0xf0}}, // WBBBBBBWWWWW
		{14, []byte{0xe7, 0x80}}, // WBW..WBBW...
		{18, []byte{0x01, 0x80}}, // .......WW...
	} {
		if got := rect.Data[tt.row*2 : tt.row*2+2]; !bytes.Equal(got, tt.want) {
			t.Errorf("mask row %d = % x, want % x", tt.row, got, tt.want)
		}
	}

	var buf bytes.Buffer
	if err := WriteFramebufferUpdate(&buf, rect); err != nil {
		t.Fatal(err)
	}
	if enc := int32(binary.BigEndian.Uint32(buf.Bytes()[12:16])); enc != EncodingCursor || buf.Len() != 4+12+12*19*4+2*19 {
		t.Fatalf("update encoding %d, %d bytes", enc, buf.Len())
	}
}

func TestCursorRectMaskPaddingAndFormat(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 9, 2))
	img.SetRGBA(0, 0, color.RGBA{R: 0xff, A: 0xff})
	img.SetRGBA(8, 0, color.RGBA{R: 0xff, A: 0xff})
	img.SetRGBA(7, 1, color.RGBA{B: 0xff, A: 0xff})
	rgb565 := PixelFormat{BitsPerPixel: 16, Depth: 16, TrueColour: true, RedMax: 31, GreenMax: 63, BlueMax: 31, RedShift: 11, GreenShift: 5, BlueShift: 0}
	rect, err := CursorRect(img, 4, 1, rgb565)
	if err != nil {
		t.Fatal(err)
	}
	if rect.X != 4 || rect.Y != 1 {
		t.Fatalf("hotspot %d,%d", rect.X, rect.Y)
	}
	if len(rect.Prefix) != 9*2*2 {
		t.Fatalf("pixels %d bytes, want 2 bytes per pixel", len(rect.Prefix))
	}
	if v := binary.LittleEndian.Uint16(rect.Prefix); v != 31<<11 {
		t.Fatalf("red pixel %04x", v)
	}
	if want := []byte{0x80, 0x80, 0x01, 0x00}; !bytes.Equal(rect.Data, want) {
		t.Fatalf("mask % x, want % x", rect.Data, want)
	}
}

func TestCursorRectRejects(t *testing.T) {
	if _, err := CursorRect(testArrow(), 12, 0, DefaultPixelFormat()); err == nil {
		t.Fatal("hotspot outside the cursor must fail")
	}
	if _, err := CursorRect(testArrow(), 0, 0, PixelFormat{BitsPerPixel: 8}); err == nil {
		t.Fatal("colour-map format must fail")
	}
}
