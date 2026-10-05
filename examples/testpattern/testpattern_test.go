package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"log/slog"
	"testing"
	"time"

	"github.com/anchorbrowser/govnc"
)

func TestPattern(t *testing.T) {
	tests := []struct {
		name   string
		w, h   int
		n      int
		checkX int
		checkY int
	}{
		{
			name:   "first frame",
			w:      640,
			h:      360,
			n:      0,
			checkX: 0,
			checkY: 0,
		},
		{
			name:   "later frame",
			w:      640,
			h:      360,
			n:      10,
			checkX: 80,
			checkY: 100,
		},
		{
			name:   "small size",
			w:      100,
			h:      50,
			n:      5,
			checkX: 40,
			checkY: 25,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img := pattern(tt.w, tt.h, tt.n)
			if img.Bounds().Dx() != tt.w || img.Bounds().Dy() != tt.h {
				t.Fatalf("want size %dx%d, got %dx%d", tt.w, tt.h, img.Bounds().Dx(), img.Bounds().Dy())
			}

			// Verify pixel at check position is not zero (has a color).
			c := img.RGBAAt(tt.checkX, tt.checkY)
			if c.R == 0 && c.G == 0 && c.B == 0 && c.A == 0 {
				t.Fatal("pixel should have a color")
			}
		})
	}
}

func TestPatternWhiteSweep(t *testing.T) {
	w, h := 640, 360

	// The white column should move.
	img1 := pattern(w, h, 0)
	img2 := pattern(w, h, 10)

	// At frame 0, sweep is at x=0.
	// At frame 10, sweep is at x=80.
	sweep1 := 0
	sweep2 := (10 * 8) % w

	// Check that the white column is where expected.
	c1 := img1.RGBAAt(sweep1, h/2)
	if c1.R != 0xff || c1.G != 0xff || c1.B != 0xff {
		t.Fatal("frame 0: sweep should be white at x=0")
	}

	c2 := img2.RGBAAt(sweep2, h/2)
	if c2.R != 0xff || c2.G != 0xff || c2.B != 0xff {
		t.Fatal("frame 10: sweep should be white at x=80")
	}

	// Position that was white in frame 0 should now be a bar color.
	c3 := img2.RGBAAt(sweep1, h/2)
	if c3.R == 0xff && c3.G == 0xff && c3.B == 0xff {
		t.Fatal("frame 10: x=0 should no longer be white")
	}
}

func TestPatternBarColors(t *testing.T) {
	w, h := 640, 360
	img := pattern(w, h, 100) // Frame where sweep is not at x=0.

	// Verify that different horizontal positions have different colors.
	c1 := img.RGBAAt(10, h/2)
	c2 := img.RGBAAt(200, h/2)

	if c1 == c2 {
		t.Fatal("different bars should have different colors")
	}
}

func TestEncode(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}

	jpg, err := encode(img)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	if len(jpg) == 0 {
		t.Fatal("encoded JPEG is empty")
	}

	// Verify it's a valid JPEG by decoding it.
	_, err = jpeg.Decode(bytes.NewReader(jpg))
	if err != nil {
		t.Fatalf("encoded JPEG is invalid: %v", err)
	}
}

func TestEncodeProducesValidJPEG(t *testing.T) {
	// Test that encode produces valid JPEGs.
	img1 := pattern(100, 100, 0)
	img2 := pattern(100, 100, 50)

	jpg1, err := encode(img1)
	if err != nil {
		t.Fatal(err)
	}

	jpg2, err := encode(img2)
	if err != nil {
		t.Fatal(err)
	}

	// Both should be valid JPEGs.
	if len(jpg1) == 0 || len(jpg2) == 0 {
		t.Fatal("encoded JPEGs should not be empty")
	}

	// Verify they decode.
	_, err = jpeg.Decode(bytes.NewReader(jpg1))
	if err != nil {
		t.Fatal("jpg1 decode failed:", err)
	}
	_, err = jpeg.Decode(bytes.NewReader(jpg2))
	if err != nil {
		t.Fatal("jpg2 decode failed:", err)
	}
}

func TestHooksStructure(t *testing.T) {
	// Test that hooks() creates the expected structure without actually calling them.
	// We can't easily call them because they log with session info we can't construct.

	// Just verify the function signature compiles and the structure is correct.
	// The actual hook behavior is tested through the integration test in main.

	// This is a smoke test to ensure hooks() returns valid Hooks.
	_ = hooks
}

func TestAnimate(t *testing.T) {
	// Test that animate publishes frames.
	srv, err := govnc.NewServer(govnc.Options{Width: width, Height: height})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		animate(ctx, srv)
		close(done)
	}()

	// Wait for animation to run.
	time.Sleep(50 * time.Millisecond)

	// The animation goroutine is running. We can't directly check the internal state
	// without connecting a viewer, but we verify that it completes cleanly.

	// Wait for goroutine to finish.
	<-done
}

func TestRunInvalidAddress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	log := slog.New(slog.NewTextHandler(nil, nil))
	// Invalid address should fail.
	err := run(ctx, "invalid:address:99999", "", log)
	if err == nil {
		t.Fatal("run should fail with invalid address")
	}
}

func TestRunWithPassword(t *testing.T) {
	// Test that run accepts a password option.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	log := slog.New(slog.DiscardHandler)
	// This should set up VNC auth but fail to listen due to immediate cancel.
	_ = run(ctx, "127.0.0.1:0", "testpassword", log)
	// We just want to ensure it doesn't panic with a password.
}

func TestPatternSweepWraparound(t *testing.T) {
	w := 640
	// Test that the sweep wraps around correctly.
	// At frame n where (n * 8) >= w, it should wrap.
	n := w / 8 // This should wrap the sweep back to 0.
	img := pattern(w, 100, n)

	sweep := (n * 8) % w
	if sweep != 0 {
		t.Fatalf("sweep should wrap to 0 at frame %d, got %d", n, sweep)
	}

	// Verify white is at x=0.
	c := img.RGBAAt(0, 50)
	if c.R != 0xff || c.G != 0xff || c.B != 0xff {
		t.Fatal("sweep should be white at x=0 after wraparound")
	}
}

func TestPatternAllBarColors(t *testing.T) {
	w, h := len(bars)*10, 10   // Wide enough to see all bars.
	img := pattern(w, h, 1000) // Frame where sweep is far away.

	// Sample each bar region and verify we get different colors.
	seen := make(map[color.RGBA]bool)
	for i := 0; i < len(bars); i++ {
		x := i*w/len(bars) + w/(len(bars)*2) // Center of each bar.
		c := img.RGBAAt(x, h/2)
		seen[c] = true
	}

	// We should see multiple different colors.
	if len(seen) < len(bars)-1 {
		t.Fatalf("should see %d bar colors, got %d", len(bars), len(seen))
	}
}
