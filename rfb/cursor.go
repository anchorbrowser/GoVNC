package rfb

import (
	"fmt"
	"image"
)

// EncodingCursor is the Cursor pseudo-encoding (RFC 6143 7.8.1): x/y carry the hotspot,
// then the cursor pixels in the client's pixel format, then a 1 bpp mask.
const EncodingCursor int32 = -239

// CursorRect encodes img as a Cursor pseudo-rectangle: the pixels in pf, then the mask, one bit
// per pixel, rows padded to whole bytes, leftmost pixel in the high bit, set where img is opaque.
func CursorRect(img *image.RGBA, hotX, hotY int, pf PixelFormat) (Rect, error) {
	if err := pf.Validate(); err != nil {
		return Rect{}, err
	}
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if w <= 0 || h <= 0 || hotX < 0 || hotY < 0 || hotX >= w || hotY >= h {
		return Rect{}, fmt.Errorf("rfb: bad cursor %dx%d hotspot %d,%d", w, h, hotX, hotY)
	}
	rowLen := (w + 7) / 8
	mask := make([]byte, rowLen*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if img.RGBAAt(img.Rect.Min.X+x, img.Rect.Min.Y+y).A != 0 {
				mask[y*rowLen+x/8] |= 0x80 >> (x % 8)
			}
		}
	}
	return Rect{
		X: uint16(hotX), Y: uint16(hotY), Width: uint16(w), Height: uint16(h),
		Encoding: EncodingCursor, Prefix: encodePixels(img, pf), Data: mask,
	}, nil
}
