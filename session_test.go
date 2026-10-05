package govnc

import (
	"context"
	"image"
	"image/color"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/anchorbrowser/govnc/rfb"
)

func TestSessionGetters(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hs := rfb.Handshake{Version: rfb.Version38, Security: rfb.SecurityVNCAuth}
	s := newSession(ctx, &Server{}, server, slog.New(slog.DiscardHandler), hs, "192.168.1.1:5900", 640, 480)

	eq(t, "192.168.1.1:5900", s.RemoteAddr())
	eq(t, rfb.Version38, s.Version())
	eq(t, rfb.SecurityVNCAuth, s.SecurityType())
	eq(t, rfb.DefaultPixelFormat(), s.PixelFormat())
	eq(t, []int32(nil), s.Encodings())
	eq(t, rfb.EncodingRaw, s.OverlayEncoding())

	if s.Context().Err() != nil {
		t.Fatal("live session context should not be done")
	}
}

func TestSessionContextEndsOnClose(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	ctx := context.Background()
	s := newSession(ctx, &Server{}, server, slog.New(slog.DiscardHandler), rfb.Handshake{}, "test", 100, 100)

	if s.Context().Err() != nil {
		t.Fatal("context should not be done yet")
	}

	noErr(t, s.Close())

	// Give it a moment to propagate.
	time.Sleep(10 * time.Millisecond)
}

func TestSessionSendClipboard(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx := context.Background()
	s := newSession(ctx, &Server{}, server, slog.New(slog.DiscardHandler), rfb.Handshake{}, "test", 100, 100)

	// Classic mode by default.
	queued := s.SendClipboard("test")
	eq(t, true, queued)

	msgs := s.clip.take()
	eq(t, 1, len(msgs))
}

func TestSessionSendClipboardExtendedNoSupport(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx := context.Background()
	srv := &Server{}
	s := newSession(ctx, srv, server, slog.New(slog.DiscardHandler), rfb.Handshake{}, "test", 100, 100)

	// Enable extended but set client caps to not support text.
	s.clip.setEncodings([]int32{rfb.EncodingExtendedClipboard})
	s.clip.take()
	s.clip.mu.Lock()
	s.clip.clientText = false
	s.clip.mu.Unlock()

	queued := s.SendClipboard("test")
	eq(t, false, queued)
}

func TestSessionSetOverlays(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx := context.Background()
	srv := &Server{}
	s := newSession(ctx, srv, server, slog.New(slog.DiscardHandler), rfb.Handshake{}, "test", 100, 100)

	overlay1 := func(fb image.Rectangle) image.Image {
		return solid(10, 10, color.RGBA{R: 255, A: 255})
	}
	overlay2 := func(fb image.Rectangle) image.Image {
		return solid(5, 5, color.RGBA{G: 255, A: 255})
	}

	s.SetOverlays(overlay1, overlay2)

	s.mu.Lock()
	eq(t, 2, len(s.overlays))
	eq(t, uint64(1), s.overlayGen)
	s.mu.Unlock()
}

func TestSessionSetOverlaysRepaintsOnChange(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx := context.Background()
	srv := &Server{}
	s := newSession(ctx, srv, server, slog.New(slog.DiscardHandler), rfb.Handshake{}, "test", 100, 100)

	overlay := func(fb image.Rectangle) image.Image {
		return solid(10, 10, color.RGBA{R: 255, A: 255})
	}

	// Set initial overlays.
	s.SetOverlays(overlay)
	s.mu.Lock()
	eq(t, false, s.full) // No repaint needed for first set on empty.
	s.mu.Unlock()

	// Replacing them should trigger a repaint.
	s.SetOverlays(overlay)
	s.mu.Lock()
	eq(t, true, s.full)
	s.mu.Unlock()
}

func TestSessionSetOverlaysRemove(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx := context.Background()
	srv := &Server{}
	s := newSession(ctx, srv, server, slog.New(slog.DiscardHandler), rfb.Handshake{}, "test", 100, 100)

	overlay := func(fb image.Rectangle) image.Image {
		return solid(10, 10, color.RGBA{R: 255, A: 255})
	}

	s.SetOverlays(overlay)
	s.mu.Lock()
	eq(t, 1, len(s.overlays))
	s.mu.Unlock()

	// Remove overlays.
	s.SetOverlays()
	s.mu.Lock()
	eq(t, 0, len(s.overlays))
	eq(t, true, s.full) // Removing triggers repaint.
	s.mu.Unlock()
}

func TestSessionRefresh(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx := context.Background()
	srv := &Server{}
	s := newSession(ctx, srv, server, slog.New(slog.DiscardHandler), rfb.Handshake{}, "test", 100, 100)

	s.mu.Lock()
	eq(t, false, s.full)
	s.mu.Unlock()

	s.Refresh()

	s.mu.Lock()
	eq(t, true, s.full)
	s.mu.Unlock()
}

func TestSessionEncodings(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx := context.Background()
	srv := &Server{opts: Options{Cursor: &Cursor{Image: solid(2, 2, color.RGBA{A: 255}), HotX: 1, HotY: 1}}}
	s := newSession(ctx, srv, server, slog.New(slog.DiscardHandler), rfb.Handshake{}, "test", 100, 100)

	encs := []int32{rfb.EncodingTight, rfb.EncodingRRE, rfb.EncodingRaw, rfb.EncodingCursor}
	s.setEncodings(encs)

	got := s.Encodings()
	eq(t, encs, got)

	// Verify it's a clone.
	got[0] = 999
	eq(t, encs, s.Encodings())
}

func TestSessionOverlayEncoding(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx := context.Background()
	srv := &Server{}
	s := newSession(ctx, srv, server, slog.New(slog.DiscardHandler), rfb.Handshake{}, "test", 100, 100)

	// Default is Raw.
	eq(t, rfb.EncodingRaw, s.OverlayEncoding())

	// RRE before Raw.
	s.setEncodings([]int32{rfb.EncodingTight, rfb.EncodingRRE, rfb.EncodingRaw})
	eq(t, rfb.EncodingRRE, s.OverlayEncoding())

	// Raw before RRE.
	s.setEncodings([]int32{rfb.EncodingTight, rfb.EncodingRaw, rfb.EncodingRRE})
	eq(t, rfb.EncodingRaw, s.OverlayEncoding())
}

func TestSessionTakeNoFrame(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx := context.Background()
	srv := &Server{}
	s := newSession(ctx, srv, server, slog.New(slog.DiscardHandler), rfb.Handshake{}, "test", 100, 100)

	// No frame published yet.
	f, full, ok := s.take()
	eq(t, false, ok)
	if f != nil {
		t.Fatal("should not return a frame")
	}
	eq(t, false, full)
}

func TestSessionTakeNoRequest(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx := context.Background()
	srv := newTestServer(t, Options{Width: 100, Height: 100})
	srv.Publish(Frame{JPEG: testJPEG(t, 100, 100, color.RGBA{A: 255})})

	s := newSession(ctx, srv, server, slog.New(slog.DiscardHandler), rfb.Handshake{}, "test", 100, 100)

	// Frame available but no request.
	f, full, ok := s.take()
	eq(t, false, ok)
	if f != nil {
		t.Fatal("should not return a frame without a request")
	}
	eq(t, false, full)
}

func TestSessionTakeFull(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx := context.Background()
	srv := newTestServer(t, Options{Width: 100, Height: 100})
	srv.Publish(Frame{JPEG: testJPEG(t, 100, 100, color.RGBA{A: 255})})

	s := newSession(ctx, srv, server, slog.New(slog.DiscardHandler), rfb.Handshake{}, "test", 100, 100)

	s.mu.Lock()
	s.pending = true
	s.full = true
	s.mu.Unlock()

	f, full, ok := s.take()
	eq(t, true, ok)
	if f == nil {
		t.Fatal("should return a frame")
	}
	eq(t, true, full)

	// Request consumed.
	s.mu.Lock()
	eq(t, false, s.pending)
	eq(t, false, s.full)
	s.mu.Unlock()
}

func TestSessionTakeIncremental(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx := context.Background()
	srv := newTestServer(t, Options{Width: 100, Height: 100})
	srv.Publish(Frame{JPEG: testJPEG(t, 100, 100, color.RGBA{A: 255})})

	s := newSession(ctx, srv, server, slog.New(slog.DiscardHandler), rfb.Handshake{}, "test", 100, 100)

	s.mu.Lock()
	s.pending = true
	s.full = false
	s.sentSeq = 0 // Frame seq will be 1.
	s.mu.Unlock()

	f, full, ok := s.take()
	eq(t, true, ok)
	if f == nil {
		t.Fatal("should return a frame")
	}
	eq(t, false, full)
}

func TestSessionTakeAlreadySent(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx := context.Background()
	srv := newTestServer(t, Options{Width: 100, Height: 100})
	srv.Publish(Frame{JPEG: testJPEG(t, 100, 100, color.RGBA{A: 255})})

	s := newSession(ctx, srv, server, slog.New(slog.DiscardHandler), rfb.Handshake{}, "test", 100, 100)

	s.mu.Lock()
	s.pending = true
	s.full = false
	s.sentSeq = 1 // Same as published frame.
	s.overlayGen = 0
	s.sentOverlayGen = 0
	s.cursorDirty = false
	s.mu.Unlock()

	// Already sent this frame, no cursor, no overlays.
	f, full, ok := s.take()
	eq(t, false, ok)
	if f != nil {
		t.Fatal("should not return already-sent frame")
	}
	eq(t, false, full)
}

func TestSessionTakeCursorDirty(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx := context.Background()
	srv := newTestServer(t, Options{Width: 100, Height: 100})
	srv.Publish(Frame{JPEG: testJPEG(t, 100, 100, color.RGBA{A: 255})})

	s := newSession(ctx, srv, server, slog.New(slog.DiscardHandler), rfb.Handshake{}, "test", 100, 100)

	s.mu.Lock()
	s.pending = true
	s.full = false
	s.sentSeq = 1
	s.cursorDirty = true
	s.mu.Unlock()

	f, full, ok := s.take()
	eq(t, true, ok)
	if f == nil {
		t.Fatal("should return frame for cursor")
	}
	eq(t, false, full)
}

func TestSessionTakeOverlayChanged(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx := context.Background()
	srv := newTestServer(t, Options{Width: 100, Height: 100})
	srv.Publish(Frame{JPEG: testJPEG(t, 100, 100, color.RGBA{A: 255})})

	s := newSession(ctx, srv, server, slog.New(slog.DiscardHandler), rfb.Handshake{}, "test", 100, 100)

	s.mu.Lock()
	s.pending = true
	s.full = false
	s.sentSeq = 1
	s.overlayGen = 2
	s.sentOverlayGen = 1
	s.mu.Unlock()

	f, full, ok := s.take()
	eq(t, true, ok)
	if f == nil {
		t.Fatal("should return frame for overlay")
	}
	eq(t, false, full)
}
