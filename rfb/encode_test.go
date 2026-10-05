package rfb

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"testing"
)

func TestAppendCompactLength(t *testing.T) {
	tests := []struct {
		n    int
		want []byte
	}{
		{0, []byte{0x00}},
		{127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}},
		{10000, []byte{0x90, 0x4e}},
		{16383, []byte{0xff, 0x7f}},
		{16384, []byte{0x80, 0x80, 0x01}},
		{TightMaxLength, []byte{0xff, 0xff, 0xff}},
	}
	for _, tt := range tests {
		got := AppendCompactLength(nil, tt.n)
		if !bytes.Equal(got, tt.want) {
			t.Errorf("AppendCompactLength(%d) = %x, want %x", tt.n, got, tt.want)
		}
		// Decode the way noVNC's tight.js _readData does.
		n := int(got[0] & 0x7f)
		if got[0]&0x80 != 0 {
			n |= int(got[1]&0x7f) << 7
			if got[1]&0x80 != 0 {
				n |= int(got[2]) << 14
			}
		}
		if n != tt.n {
			t.Errorf("round trip %d -> %d", tt.n, n)
		}
	}
}

func TestTightJPEGUpdateLayout(t *testing.T) {
	jpegBytes := bytes.Repeat([]byte{0xab}, 200)
	rect, err := TightJPEGRect(640, 480, jpegBytes)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteFramebufferUpdate(&buf, DesktopSizeRect(640, 480), rect); err != nil {
		t.Fatal(err)
	}
	want := []byte{MsgFramebufferUpdate, 0, 0, 2}
	want = append(want, 0, 0, 0, 0, 0x02, 0x80, 0x01, 0xe0)
	desktopSize := EncodingDesktopSize
	want = binary.BigEndian.AppendUint32(want, uint32(desktopSize))
	want = append(want, 0, 0, 0, 0, 0x02, 0x80, 0x01, 0xe0)
	want = binary.BigEndian.AppendUint32(want, uint32(EncodingTight))
	want = append(want, TightJPEG, 0xc8, 0x01)
	want = append(want, jpegBytes...)
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("update\n got %x\nwant %x", buf.Bytes(), want)
	}
	if _, err := TightJPEGRect(1, 1, make([]byte, TightMaxLength+1)); err == nil {
		t.Fatal("oversized jpeg must be refused")
	}
}

func tinyJPEG(t *testing.T, w, h int, c color.RGBA) image.Image {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func near(a, b, tol int) bool { return a-b <= tol && b-a <= tol }

func TestRawRectPixelConversion(t *testing.T) {
	img := tinyJPEG(t, 4, 2, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	novnc := PixelFormat{BitsPerPixel: 32, Depth: 24, TrueColour: true, RedMax: 255, GreenMax: 255, BlueMax: 255, RedShift: 0, GreenShift: 8, BlueShift: 16}
	rgb565be := PixelFormat{BitsPerPixel: 16, Depth: 16, BigEndian: true, TrueColour: true, RedMax: 31, GreenMax: 63, BlueMax: 31, RedShift: 11, GreenShift: 5, BlueShift: 0}

	t.Run("noVNC little-endian rgbx", func(t *testing.T) {
		rect, err := RawRect(img, 4, 2, novnc)
		if err != nil {
			t.Fatal(err)
		}
		if rect.Encoding != EncodingRaw || len(rect.Data) != 4*2*4 {
			t.Fatalf("rect %+v len %d", rect, len(rect.Data))
		}
		r, g, b := int(rect.Data[0]), int(rect.Data[1]), int(rect.Data[2])
		if !near(r, 200, 3) || !near(g, 100, 3) || !near(b, 50, 3) || rect.Data[3] != 0 {
			t.Fatalf("pixel 0 = % x", rect.Data[:4])
		}
	})
	t.Run("default bgrx", func(t *testing.T) {
		rect, err := RawRect(img, 4, 2, DefaultPixelFormat())
		if err != nil {
			t.Fatal(err)
		}
		if !near(int(rect.Data[2]), 200, 3) || !near(int(rect.Data[0]), 50, 3) {
			t.Fatalf("pixel 0 = % x", rect.Data[:4])
		}
	})
	t.Run("rgb565 big-endian", func(t *testing.T) {
		rect, err := RawRect(img, 4, 2, rgb565be)
		if err != nil {
			t.Fatal(err)
		}
		v := binary.BigEndian.Uint16(rect.Data)
		r, g, b := int(v>>11), int(v>>5&63), int(v&31)
		if !near(r, 200*31/255, 1) || !near(g, 100*63/255, 1) || !near(b, 50*31/255, 1) || len(rect.Data) != 4*2*2 {
			t.Fatalf("pixel %04x -> %d %d %d", v, r, g, b)
		}
	})
	t.Run("larger rect pads black", func(t *testing.T) {
		rect, err := RawRect(img, 6, 2, novnc)
		if err != nil {
			t.Fatal(err)
		}
		last := rect.Data[len(rect.Data)-4:]
		if !bytes.Equal(last, []byte{0, 0, 0, 0}) {
			t.Fatalf("padding pixel % x", last)
		}
	})
	t.Run("colour map refused", func(t *testing.T) {
		if _, err := RawRect(img, 4, 2, PixelFormat{BitsPerPixel: 8}); err == nil {
			t.Fatal("want error")
		}
	})
}

// rreToRaw paints an RRE rect the way a client does (background, then each subrect in order)
// into Raw pixel bytes in pf, so it can be compared with RawRect.
func rreToRaw(t *testing.T, rect Rect, pf PixelFormat) []byte {
	t.Helper()
	bpp := int(pf.BitsPerPixel / 8)
	w, h := int(rect.Width), int(rect.Height)
	d := rect.Data
	if len(d) < 4+bpp {
		t.Fatalf("rre data %d bytes", len(d))
	}
	n := int(binary.BigEndian.Uint32(d))
	bg := d[4 : 4+bpp]
	d = d[4+bpp:]
	if len(d) != n*(bpp+8) {
		t.Fatalf("rre: %d subrects but %d bytes left", n, len(d))
	}
	out := make([]byte, w*h*bpp)
	fill := func(px []byte, x, y, sw, sh int) {
		if x+sw > w || y+sh > h || sw == 0 || sh == 0 {
			t.Fatalf("subrect %d,%d %dx%d outside %dx%d", x, y, sw, sh, w, h)
		}
		for yy := y; yy < y+sh; yy++ {
			for xx := x; xx < x+sw; xx++ {
				copy(out[(yy*w+xx)*bpp:], px)
			}
		}
	}
	fill(bg, 0, 0, w, h)
	for i := 0; i < n; i++ {
		s := d[i*(bpp+8):]
		g := s[bpp:]
		fill(s[:bpp], int(binary.BigEndian.Uint16(g)), int(binary.BigEndian.Uint16(g[2:])), int(binary.BigEndian.Uint16(g[4:])), int(binary.BigEndian.Uint16(g[6:])))
	}
	return out
}

func TestRRERectMatchesRaw(t *testing.T) {
	novnc := PixelFormat{BitsPerPixel: 32, Depth: 24, TrueColour: true, RedMax: 255, GreenMax: 255, BlueMax: 255, RedShift: 0, GreenShift: 8, BlueShift: 16}
	rgb565be := PixelFormat{BitsPerPixel: 16, Depth: 16, BigEndian: true, TrueColour: true, RedMax: 31, GreenMax: 63, BlueMax: 31, RedShift: 11, GreenShift: 5, BlueShift: 0}
	bgr233 := PixelFormat{BitsPerPixel: 8, Depth: 8, TrueColour: true, RedMax: 7, GreenMax: 7, BlueMax: 3, RedShift: 0, GreenShift: 3, BlueShift: 6}

	menu := flatDrawing(image.Pt(40, 30), 5)
	checker := image.NewRGBA(image.Rect(0, 0, 7, 5))
	for y := 0; y < 5; y++ {
		for x := 0; x < 7; x++ {
			checker.Set(x, y, color.RGBA{R: uint8(40 * ((x + y) % 3)), G: uint8(x * 30), B: 200, A: 255})
		}
	}
	tests := []struct {
		name string
		img  image.Image
		w, h uint16
	}{
		{"flat drawing (origin not at 0,0)", menu, uint16(menu.Rect.Dx()), uint16(menu.Rect.Dy())},
		{"every pixel differs", checker, 7, 5},
		{"larger rect pads black", checker, 9, 6},
		{"smaller rect crops", menu, 20, 10},
	}
	for _, tt := range tests {
		for _, pf := range []struct {
			name string
			pf   PixelFormat
		}{{"noVNC rgbx", novnc}, {"default bgrx", DefaultPixelFormat()}, {"rgb565 big-endian", rgb565be}, {"bgr233", bgr233}} {
			t.Run(tt.name+"/"+pf.name, func(t *testing.T) {
				rre, err := RRERect(tt.img, tt.w, tt.h, pf.pf)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := RawRect(tt.img, tt.w, tt.h, pf.pf)
				if err != nil {
					t.Fatal(err)
				}
				if rre.Encoding != EncodingRRE || rre.Width != tt.w || rre.Height != tt.h || len(rre.Prefix) != 0 {
					t.Fatalf("rect %d %dx%d prefix %d", rre.Encoding, rre.Width, rre.Height, len(rre.Prefix))
				}
				if got := rreToRaw(t, rre, pf.pf); !bytes.Equal(got, raw.Data) {
					t.Fatal("RRE paints different pixels than Raw")
				}
			})
		}
	}
}

var (
	flatFill = color.RGBA{R: 0x29, G: 0x2a, B: 0x2d, A: 0xff}
	flatEdge = color.RGBA{R: 0x5f, G: 0x63, B: 0x68, A: 0xff}
	flatInk  = color.RGBA{R: 0xe8, G: 0xea, B: 0xed, A: 0xff}
)

// flatDrawing is a 180-px-wide panel at origin with a border and rows of short bars: the kind of
// flat UI drawing (a menu) RRE is for.
func flatDrawing(origin image.Point, rows int) *image.RGBA {
	const w, rowH = 180, 24
	img := image.NewRGBA(image.Rect(origin.X, origin.Y, origin.X+w, origin.Y+rows*rowH+2))
	draw.Draw(img, img.Rect, &image.Uniform{C: flatEdge}, image.Point{}, draw.Src)
	draw.Draw(img, img.Rect.Inset(1), &image.Uniform{C: flatFill}, image.Point{}, draw.Src)
	for r := 0; r < rows; r++ {
		y := origin.Y + 1 + r*rowH + 8
		for x := origin.X + 12; x < origin.X+12+(r+3)*12; x += 12 {
			draw.Draw(img, image.Rect(x, y, x+8, y+8), &image.Uniform{C: flatInk}, image.Point{}, draw.Src)
		}
	}
	return img
}

func TestRRERectIsCompactForFlatDrawings(t *testing.T) {
	menu := flatDrawing(image.Point{}, 10)
	w, h := uint16(menu.Rect.Dx()), uint16(menu.Rect.Dy())
	rre, err := RRERect(menu, w, h, DefaultPixelFormat())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := RawRect(menu, w, h, DefaultPixelFormat())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("drawing %dx%d: RRE %d bytes, Raw %d bytes", w, h, len(rre.Data), len(raw.Data))
	if len(rre.Data)*10 > len(raw.Data) {
		t.Fatalf("RRE drawing is %d bytes, Raw %d: want at most a tenth", len(rre.Data), len(raw.Data))
	}
	// The background is the panel fill, the most common colour.
	if got := rre.Data[4:7]; !bytes.Equal(got, []byte{flatFill.B, flatFill.G, flatFill.R}) {
		t.Fatalf("background % x, want the panel fill", got)
	}
}

func TestRRERectRefusesAColourMap(t *testing.T) {
	if _, err := RRERect(image.NewRGBA(image.Rect(0, 0, 2, 2)), 2, 2, PixelFormat{BitsPerPixel: 8}); err == nil {
		t.Fatal("want error")
	}
}
