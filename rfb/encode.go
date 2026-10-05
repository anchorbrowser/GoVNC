package rfb

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"io"
	"net"
)

// Rect is one FramebufferUpdate rectangle. Prefix and Data are written after the 12-byte
// header without being copied together, so a JPEG goes out verbatim.
type Rect struct {
	X, Y, Width, Height uint16
	Encoding            int32
	Prefix              []byte
	Data                []byte
}

const (
	updateHeaderLen = 4
	rectHeaderLen   = 12
)

// AppendCompactLength appends n in Tight's compact form: 7 bits per byte, low bits first, the
// top bit of a byte set when another byte follows; the third byte carries 8 bits.
func AppendCompactLength(dst []byte, n int) []byte {
	b := byte(n & 0x7f)
	if n <= 0x7f {
		return append(dst, b)
	}
	dst = append(dst, b|0x80)
	b = byte(n >> 7 & 0x7f)
	if n <= 0x3fff {
		return append(dst, b)
	}
	return append(dst, b|0x80, byte(n>>14))
}

// TightJPEGRect wraps a JPEG as one Tight rectangle of width x height at the origin.
func TightJPEGRect(width, height uint16, jpeg []byte) (Rect, error) {
	if len(jpeg) == 0 || len(jpeg) > TightMaxLength {
		return Rect{}, fmt.Errorf("rfb: jpeg of %d bytes does not fit a tight rect", len(jpeg))
	}
	prefix := AppendCompactLength([]byte{TightJPEG}, len(jpeg))
	return Rect{Width: width, Height: height, Encoding: EncodingTight, Prefix: prefix, Data: jpeg}, nil
}

// DesktopSizeRect announces a new framebuffer size.
func DesktopSizeRect(width, height uint16) Rect {
	return Rect{Width: width, Height: height, Encoding: EncodingDesktopSize}
}

// RawRect converts img to pf as one Raw rectangle of width x height at the origin. Pixels outside
// img are black; img larger than the rectangle is cropped.
func RawRect(img image.Image, width, height uint16, pf PixelFormat) (Rect, error) {
	if err := pf.Validate(); err != nil {
		return Rect{}, err
	}
	return Rect{Width: width, Height: height, Encoding: EncodingRaw, Data: encodePixels(atOrigin(img, width, height), pf)}, nil
}

// atOrigin copies img into a width x height RGBA at the origin: black outside img, cropped.
func atOrigin(img image.Image, width, height uint16) *image.RGBA {
	rgba := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
	if img != nil {
		draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)
	}
	return rgba
}

// rreHeaderLen is the subrect count; rreSubrectGeomLen is x, y, w, h after each subrect's pixel.
const (
	rreHeaderLen      = 4
	rreSubrectGeomLen = 8
)

// rreRun is one subrect: a run of one colour, grown downwards while the rows below repeat it.
type rreRun struct {
	colour     uint32
	x, y, w, h int
}

// RRERect converts img to pf as one RRE rectangle (RFC 6143 7.7.3) of width x height at the
// origin, cropped like RawRect. The most common colour is the background; each run of another
// colour is a subrect, merged with the same run in the rows below. Lossless, and small for flat
// drawings (the context menu).
func RRERect(img image.Image, width, height uint16, pf PixelFormat) (Rect, error) {
	if err := pf.Validate(); err != nil {
		return Rect{}, err
	}
	rgba := atOrigin(img, width, height)
	w, h := int(width), int(height)
	colourAt := func(x, y int) uint32 {
		i := y*rgba.Stride + x*4
		return uint32(rgba.Pix[i])<<16 | uint32(rgba.Pix[i+1])<<8 | uint32(rgba.Pix[i+2])
	}
	counts := map[uint32]int{}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			counts[colourAt(x, y)]++
		}
	}
	var bg uint32
	best := -1
	for c, n := range counts {
		if n > best || (n == best && c < bg) {
			bg, best = c, n
		}
	}
	type runKey struct {
		colour uint32
		x, w   int
	}
	var runs []rreRun
	open := map[runKey]int{}
	for y := 0; y < h; y++ {
		next := map[runKey]int{}
		for x := 0; x < w; {
			c := colourAt(x, y)
			end := x + 1
			for end < w && colourAt(end, y) == c {
				end++
			}
			if c != bg {
				k := runKey{c, x, end - x}
				if i, ok := open[k]; ok {
					runs[i].h++
					next[k] = i
				} else {
					next[k] = len(runs)
					runs = append(runs, rreRun{colour: c, x: x, y: y, w: end - x, h: 1})
				}
			}
			x = end
		}
		open = next
	}
	bytesPP := int(pf.BitsPerPixel / 8)
	out := make([]byte, 0, rreHeaderLen+bytesPP+len(runs)*(bytesPP+rreSubrectGeomLen))
	out = binary.BigEndian.AppendUint32(out, uint32(len(runs)))
	out = appendPixel(out, bg, pf)
	for _, r := range runs {
		out = appendPixel(out, r.colour, pf)
		out = binary.BigEndian.AppendUint16(out, uint16(r.x))
		out = binary.BigEndian.AppendUint16(out, uint16(r.y))
		out = binary.BigEndian.AppendUint16(out, uint16(r.w))
		out = binary.BigEndian.AppendUint16(out, uint16(r.h))
	}
	return Rect{Width: width, Height: height, Encoding: EncodingRRE, Data: out}, nil
}

// appendPixel appends the 0xRRGGBB colour c in pf.
func appendPixel(dst []byte, c uint32, pf PixelFormat) []byte {
	r, g, b := uint8(c>>16), uint8(c>>8), uint8(c)
	v := scale(r, pf.RedMax)<<pf.RedShift | scale(g, pf.GreenMax)<<pf.GreenShift | scale(b, pf.BlueMax)<<pf.BlueShift
	px := make([]byte, pf.BitsPerPixel/8)
	putPixel(px, v, pf.BigEndian)
	return append(dst, px...)
}

func encodePixels(img *image.RGBA, pf PixelFormat) []byte {
	bytesPP := int(pf.BitsPerPixel / 8)
	w, h := img.Rect.Dx(), img.Rect.Dy()
	out := make([]byte, w*h*bytesPP)
	o := 0
	fast := bytesPP == 4 && !pf.BigEndian && pf.RedMax == 255 && pf.GreenMax == 255 && pf.BlueMax == 255
	for y := 0; y < h; y++ {
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for x := 0; x < w*4; x += 4 {
			r, g, b := row[x], row[x+1], row[x+2]
			if fast {
				v := uint32(r)<<pf.RedShift | uint32(g)<<pf.GreenShift | uint32(b)<<pf.BlueShift
				binary.LittleEndian.PutUint32(out[o:], v)
				o += 4
				continue
			}
			v := scale(r, pf.RedMax)<<pf.RedShift | scale(g, pf.GreenMax)<<pf.GreenShift | scale(b, pf.BlueMax)<<pf.BlueShift
			putPixel(out[o:o+bytesPP], v, pf.BigEndian)
			o += bytesPP
		}
	}
	return out
}

func scale(c uint8, max uint16) uint32 {
	return (uint32(c)*uint32(max) + 127) / 255
}

func putPixel(dst []byte, v uint32, bigEndian bool) {
	switch len(dst) {
	case 1:
		dst[0] = byte(v)
	case 2:
		if bigEndian {
			binary.BigEndian.PutUint16(dst, uint16(v))
		} else {
			binary.LittleEndian.PutUint16(dst, uint16(v))
		}
	case 4:
		if bigEndian {
			binary.BigEndian.PutUint32(dst, v)
		} else {
			binary.LittleEndian.PutUint32(dst, v)
		}
	}
}

// WriteFramebufferUpdate writes one FramebufferUpdate with rects, in order.
func WriteFramebufferUpdate(w io.Writer, rects ...Rect) error {
	head := make([]byte, 0, updateHeaderLen+rectHeaderLen)
	head = append(head, MsgFramebufferUpdate, 0)
	head = binary.BigEndian.AppendUint16(head, uint16(len(rects)))
	bufs := make(net.Buffers, 0, 1+3*len(rects))
	for _, r := range rects {
		head = binary.BigEndian.AppendUint16(head, r.X)
		head = binary.BigEndian.AppendUint16(head, r.Y)
		head = binary.BigEndian.AppendUint16(head, r.Width)
		head = binary.BigEndian.AppendUint16(head, r.Height)
		head = binary.BigEndian.AppendUint32(head, uint32(r.Encoding))
		bufs = append(bufs, head)
		head = make([]byte, 0, rectHeaderLen)
		if len(r.Prefix) > 0 {
			bufs = append(bufs, r.Prefix)
		}
		if len(r.Data) > 0 {
			bufs = append(bufs, r.Data)
		}
	}
	if len(rects) == 0 {
		bufs = append(bufs, head)
	}
	if _, err := bufs.WriteTo(w); err != nil {
		return fmt.Errorf("rfb: writing framebuffer update: %w", err)
	}
	return nil
}
