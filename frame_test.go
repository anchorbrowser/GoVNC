package govnc

import (
	"image"
	"image/color"
	"math"
	"sync"
	"testing"
)

func TestFrameSize(t *testing.T) {
	tests := []struct {
		name  string
		frame Frame
		wantW int
		wantH int
	}{
		{
			name:  "explicit size",
			frame: Frame{Width: 100, Height: 200},
			wantW: 100,
			wantH: 200,
		},
		{
			name:  "from Image",
			frame: Frame{Image: solid(50, 30, color.RGBA{A: 255})},
			wantW: 50,
			wantH: 30,
		},
		{
			name:  "from JPEG header",
			frame: Frame{JPEG: testJPEG(t, 64, 48, color.RGBA{R: 255, A: 255})},
			wantW: 64,
			wantH: 48,
		},
		{
			name:  "explicit overrides Image",
			frame: Frame{Image: solid(10, 10, color.RGBA{A: 255}), Width: 40, Height: 50},
			wantW: 40,
			wantH: 50,
		},
		{
			name:  "explicit overrides JPEG",
			frame: Frame{JPEG: testJPEG(t, 10, 10, color.RGBA{A: 255}), Width: 80, Height: 90},
			wantW: 80,
			wantH: 90,
		},
		{
			name:  "bad JPEG falls back to explicit",
			frame: Frame{JPEG: []byte("not a jpeg"), Width: 20, Height: 30},
			wantW: 20,
			wantH: 30,
		},
		{
			name:  "no picture no size",
			frame: Frame{},
			wantW: 0,
			wantH: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, h := frameSize(tt.frame)
			eq(t, tt.wantW, w)
			eq(t, tt.wantH, h)
		})
	}
}

func TestClampFB(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{"zero clamped to 1", 0, 1},
		{"negative clamped to 1", -100, 1},
		{"max uint16", math.MaxUint16, math.MaxUint16},
		{"over max clamped", math.MaxUint16 + 1, math.MaxUint16},
		{"way over max clamped", 1 << 20, math.MaxUint16},
		{"normal value unchanged", 1920, 1920},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eq(t, tt.want, clampFB(tt.in))
		})
	}
}

func TestPublishedImageDecodesOnce(t *testing.T) {
	jpg := testJPEG(t, 16, 16, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	p := newPublished(1, Frame{JPEG: jpg})

	// Concurrent callers all get the same decode.
	var wg sync.WaitGroup
	results := make([]image.Image, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			img, err := p.image()
			noErr(t, err)
			results[idx] = img
		}(i)
	}
	wg.Wait()

	// All results should be the same pointer, proving single decode.
	first := results[0]
	for i, img := range results {
		if img != first {
			t.Fatalf("result %d is a different image pointer", i)
		}
	}

	// Subsequent calls also get the same cached result.
	again, err := p.image()
	noErr(t, err)
	if again != first {
		t.Fatal("cached image not reused")
	}
}

func TestPublishedImageFromImage(t *testing.T) {
	img := solid(8, 8, color.RGBA{R: 255, A: 255})
	p := newPublished(1, Frame{Image: img})

	got, err := p.image()
	noErr(t, err)
	if got != img {
		t.Fatal("image not returned directly")
	}
}

func TestPublishedImageNoPicture(t *testing.T) {
	p := newPublished(1, Frame{Width: 10, Height: 10})

	_, err := p.image()
	if err != errNoPicture {
		t.Fatalf("want errNoPicture, got %v", err)
	}

	// Error is cached too.
	_, err = p.image()
	if err != errNoPicture {
		t.Fatal("error not cached")
	}
}

func TestPublishedImageBadJPEG(t *testing.T) {
	p := newPublished(1, Frame{JPEG: []byte("not a jpeg")})

	_, err := p.image()
	if err == nil {
		t.Fatal("want decode error")
	}

	// Error is cached.
	_, err2 := p.image()
	if err2 != err {
		t.Fatal("error not cached")
	}
}

func TestNewPublishedClampsSizeAndSetsSeq(t *testing.T) {
	p := newPublished(42, Frame{Width: 1 << 20, Height: 3})
	eq(t, uint64(42), p.seq)
	eq(t, math.MaxUint16, p.width)
	eq(t, 3, p.height)
}

func TestPublishedImageConcurrentDecodeDifferentJPEGs(t *testing.T) {
	// Two different frames decode independently.
	jpg1 := testJPEG(t, 8, 8, color.RGBA{R: 255, A: 255})
	jpg2 := testJPEG(t, 8, 8, color.RGBA{G: 255, A: 255})
	p1 := newPublished(1, Frame{JPEG: jpg1})
	p2 := newPublished(2, Frame{JPEG: jpg2})

	img1, err := p1.image()
	noErr(t, err)
	img2, err := p2.image()
	noErr(t, err)

	if img1 == img2 {
		t.Fatal("two different frames must decode to different images")
	}

	// Verify the images are actually different by comparing their bounds and colors.
	c1 := img1.At(0, 0)
	c2 := img2.At(0, 0)
	r1, g1, b1, _ := c1.RGBA()
	r2, g2, b2, _ := c2.RGBA()
	if r1 == r2 && g1 == g2 && b1 == b2 {
		t.Fatal("images should have different colors")
	}
}
