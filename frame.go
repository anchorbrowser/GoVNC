package govnc

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"math"
	"sync"
)

// Frame is one picture of the framebuffer.
type Frame struct {
	// JPEG goes verbatim to clients that take Tight JPEG at a matching size; others get it
	// decoded once (shared by every viewer) and converted to their pixel format.
	JPEG []byte
	// Image is the picture when JPEG is nil. It is sent as Raw, or RRE to a client that prefers
	// RRE (cheap for flat drawings, expensive for photos: publish a JPEG for those).
	Image image.Image
	// Width and Height are the framebuffer size; when either is 0 it is taken from Image or the
	// JPEG header.
	Width, Height int
}

var errNoPicture = errors.New("govnc: frame has no picture")

// published is a frame with its sequence number and its decode, done at most once.
type published struct {
	seq           uint64
	frame         Frame
	width, height int

	once sync.Once
	img  image.Image
	err  error
}

func newPublished(seq uint64, f Frame) *published {
	w, h := frameSize(f)
	return &published{seq: seq, frame: f, width: clampFB(w), height: clampFB(h)}
}

func (p *published) image() (image.Image, error) {
	p.once.Do(func() {
		switch {
		case p.frame.JPEG != nil:
			p.img, p.err = jpeg.Decode(bytes.NewReader(p.frame.JPEG))
		case p.frame.Image != nil:
			p.img = p.frame.Image
		default:
			p.err = errNoPicture
		}
	})
	return p.img, p.err
}

func frameSize(f Frame) (int, int) {
	if f.Width > 0 && f.Height > 0 {
		return f.Width, f.Height
	}
	if f.JPEG != nil {
		if cfg, err := jpeg.DecodeConfig(bytes.NewReader(f.JPEG)); err == nil {
			return cfg.Width, cfg.Height
		}
	}
	if f.Image != nil {
		b := f.Image.Bounds()
		return b.Dx(), b.Dy()
	}
	return f.Width, f.Height
}

func clampFB(n int) int {
	return max(1, min(n, math.MaxUint16))
}
