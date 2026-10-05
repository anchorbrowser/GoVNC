package govnc

import (
	"image"
	"image/draw"

	"github.com/anchorbrowser/govnc/rfb"
)

// Overlay draws over the frame, for example a menu. It is called on the session's writer with
// the framebuffer the viewer knows, when the overlay is new or that size changed, and returns the
// image to draw at its bounds (clipped to fb); nil draws nothing. The drawing is re-sent after
// every page repaint until the session's overlays are replaced.
type Overlay func(fb image.Rectangle) image.Image

// prefersRRE reports whether the client lists RRE before Raw (its preference order).
func prefersRRE(encodings []int32) bool {
	for _, e := range encodings {
		switch e {
		case rfb.EncodingRRE:
			return true
		case rfb.EncodingRaw:
			return false
		}
	}
	return false
}

// encodeAt encodes the part of img inside fb as one rect at its position, in enc (RRE or Raw).
// ok is false when nothing of img is inside fb.
func encodeAt(img image.Image, fb image.Rectangle, pf rfb.PixelFormat, enc int32) (rfb.Rect, bool, error) {
	b := img.Bounds().Intersect(fb)
	if b.Empty() {
		return rfb.Rect{}, false, nil
	}
	if b != img.Bounds() {
		clip := image.NewRGBA(b)
		draw.Draw(clip, b, img, b.Min, draw.Src)
		img = clip
	}
	var (
		r   rfb.Rect
		err error
	)
	if enc == rfb.EncodingRRE {
		r, err = rfb.RRERect(img, uint16(b.Dx()), uint16(b.Dy()), pf)
	} else {
		r, err = rfb.RawRect(img, uint16(b.Dx()), uint16(b.Dy()), pf)
	}
	if err != nil {
		return rfb.Rect{}, false, err
	}
	r.X, r.Y = uint16(b.Min.X), uint16(b.Min.Y)
	return r, true, nil
}
