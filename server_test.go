package govnc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anchorbrowser/govnc/rfb"
)

func TestHandshakeAnnouncesOptionsSizeAndWaitsForAFrame(t *testing.T) {
	srv := newTestServer(t, Options{Width: 64, Height: 48, Name: "desk"})
	c := dial(t, srv, rfb.EncodingTight)
	eq(t, 64, c.width)
	eq(t, 48, c.height)
	eq(t, "desk", c.name)
	waitFor(t, "the session", func() bool { return srv.NumSessions() == 1 })

	c.request(false)
	c.expectSilence(100 * time.Millisecond)
	jpg := testJPEG(t, 64, 48, color.RGBA{R: 255, A: 255})
	srv.Publish(Frame{JPEG: jpg})
	eq(t, []int32{rfb.EncodingTight}, encodings(c.readUpdate()))
}

func TestDefaultsAndDerivedFrameSize(t *testing.T) {
	srv, err := NewServer(Options{})
	noErr(t, err)
	eq(t, DefaultName, srv.opts.Name)
	eq(t, DefaultHandshakeTimeout, srv.opts.HandshakeTimeout)
	eq(t, DefaultWriteTimeout, srv.opts.WriteTimeout)
	w, h := srv.size()
	eq(t, [2]int{1, 1}, [2]int{w, h})

	srv.Publish(Frame{JPEG: testJPEG(t, 30, 20, color.RGBA{A: 255})})
	w, h = srv.size()
	eq(t, [2]int{30, 20}, [2]int{w, h})
	srv.Publish(Frame{Image: solid(7, 5, color.RGBA{A: 255})})
	w, h = srv.size()
	eq(t, [2]int{7, 5}, [2]int{w, h})
	srv.Publish(Frame{Width: 1 << 20, Height: 3})
	w, h = srv.size()
	eq(t, [2]int{65535, 3}, [2]int{w, h})
}

func TestNewServerValidates(t *testing.T) {
	tests := []struct {
		name string
		opts Options
	}{
		{"negative max", Options{MaxSessions: -1}},
		{"nil security", Options{Security: []rfb.SecurityType{nil}}},
		{"vnc auth without a password", Options{Security: []rfb.SecurityType{rfb.VNCAuth{}}}},
		{"vnc auth pointer without a password", Options{Security: []rfb.SecurityType{&rfb.VNCAuth{}}}},
		{"cursor without image", Options{Cursor: &Cursor{}}},
		{"cursor hotspot outside", Options{Cursor: &Cursor{Image: solid(2, 2, color.RGBA{A: 255}), HotX: 2}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewServer(tt.opts); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

func TestTightSendsTheJPEGVerbatimAndResizes(t *testing.T) {
	srv := newTestServer(t, Options{})
	first := testJPEG(t, 32, 16, color.RGBA{R: 255, A: 255})
	srv.Publish(Frame{JPEG: first, Width: 32, Height: 16})
	c := dial(t, srv, rfb.EncodingTight, rfb.EncodingRaw, rfb.EncodingDesktopSize)
	eq(t, 32, c.width)
	c.request(false)
	rects := c.readUpdate()
	eq(t, []int32{rfb.EncodingTight}, encodings(rects))
	if !bytes.Equal(first, rects[0].data) {
		t.Fatal("the tight rect must carry the published JPEG byte for byte")
	}

	c.request(true)
	c.expectSilence(100 * time.Millisecond)
	bigger := testJPEG(t, 40, 20, color.RGBA{G: 255, A: 255})
	srv.Publish(Frame{JPEG: bigger})
	rects = c.readUpdate()
	eq(t, []int32{rfb.EncodingDesktopSize, rfb.EncodingTight}, encodings(rects))
	eq(t, [2]int{40, 20}, [2]int{rects[0].w, rects[0].h})
	if !bytes.Equal(bigger, rects[1].data) {
		t.Fatal("resized frame must be verbatim too")
	}
}

var noVNCPixelFormat = []byte{32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 0, 8, 16, 0, 0, 0}

func near(a, b int) bool { return a-b <= 4 && b-a <= 4 }

func TestRawFallbackCropsWithoutDesktopSize(t *testing.T) {
	srv := newTestServer(t, Options{})
	srv.Publish(Frame{JPEG: testJPEG(t, 8, 4, color.RGBA{R: 200, G: 100, B: 50, A: 255})})
	c := dial(t, srv)
	c.setPixelFormat(noVNCPixelFormat)
	c.setEncodings(rfb.EncodingRaw)
	c.request(false)
	rects := c.readUpdate()
	eq(t, []int32{rfb.EncodingRaw}, encodings(rects))
	px := rects[0].data[:4]
	if !near(int(px[0]), 200) || !near(int(px[1]), 100) || !near(int(px[2]), 50) {
		t.Fatalf("raw pixel % x not in the client's rgbx order", px)
	}

	srv.Publish(Frame{JPEG: testJPEG(t, 16, 8, color.RGBA{B: 255, A: 255})})
	c.request(true)
	rects = c.readUpdate()
	eq(t, [2]int{8, 4}, [2]int{rects[0].w, rects[0].h})
}

func TestImageFramesAreRawOrRREByPreference(t *testing.T) {
	srv := newTestServer(t, Options{})
	srv.Publish(Frame{Image: solid(6, 3, color.RGBA{R: 9, G: 8, B: 7, A: 255})})
	raw := dial(t, srv, rfb.EncodingTight, rfb.EncodingRaw, rfb.EncodingRRE)
	raw.request(false)
	eq(t, []int32{rfb.EncodingRaw}, encodings(raw.readUpdate()))

	rre := dial(t, srv, rfb.EncodingTight, rfb.EncodingRRE, rfb.EncodingRaw)
	rre.request(false)
	rects := rre.readUpdate()
	eq(t, []int32{rfb.EncodingRRE}, encodings(rects))
	eq(t, []byte{0, 0, 0, 0, 7, 8, 9, 0}, rects[0].data)
}

func TestFrameWithoutAPictureSendsNothing(t *testing.T) {
	srv := newTestServer(t, Options{})
	srv.Publish(Frame{Width: 4, Height: 4})
	c := dial(t, srv, rfb.EncodingRaw)
	c.request(false)
	c.expectSilence(100 * time.Millisecond)
	// The request stays open for the next frame.
	srv.Publish(Frame{Image: solid(4, 4, color.RGBA{A: 255})})
	eq(t, []int32{rfb.EncodingRaw}, encodings(c.readUpdate()))
}

func TestLatestFrameWinsForASlowViewer(t *testing.T) {
	srv := newTestServer(t, Options{})
	srv.Publish(Frame{JPEG: testJPEG(t, 16, 16, color.RGBA{A: 255})})
	c := dial(t, srv, rfb.EncodingTight, rfb.EncodingDesktopSize)
	c.request(false)
	c.readUpdate()

	frames := [][]byte{testJPEG(t, 16, 16, color.RGBA{R: 10, A: 255}), testJPEG(t, 16, 16, color.RGBA{G: 10, A: 255}), testJPEG(t, 16, 16, color.RGBA{B: 10, A: 255})}
	start := time.Now()
	for i := 0; i < 3000; i++ {
		srv.Publish(Frame{JPEG: frames[i%2]})
	}
	srv.Publish(Frame{JPEG: frames[2]})
	if time.Since(start) > 2*time.Second {
		t.Fatal("Publish blocked on a viewer that is not reading")
	}
	c.request(true)
	rects := c.readUpdate()
	if !bytes.Equal(frames[2], rects[len(rects)-1].data) {
		t.Fatal("the slow viewer must get only the newest frame")
	}
	c.expectSilence(150 * time.Millisecond)
}

// recorder collects hook calls in order.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	r.calls = append(r.calls, s)
	r.mu.Unlock()
}

func (r *recorder) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func TestHookChainsStopAtTheFirstConsumer(t *testing.T) {
	rec := &recorder{}
	closed := make(chan error, 1)
	srv := newTestServer(t, Options{OnSession: func(s *Session) Hooks {
		eq(t, rfb.Version38, s.Version())
		eq(t, rfb.SecurityNone, s.SecurityType())
		return Hooks{
			Key: []KeyHandler{
				func(_ context.Context, ev rfb.KeyEvent) bool { rec.add("key1"); return false },
				func(_ context.Context, ev rfb.KeyEvent) bool { rec.add("key2"); return ev.Keysym == 'a' },
				func(_ context.Context, ev rfb.KeyEvent) bool { rec.add("key3"); return true },
			},
			Pointer: []PointerHandler{
				func(_ context.Context, ev rfb.PointerEvent) bool {
					rec.add("pointer " + string(rune('0'+ev.Mask)))
					return true
				},
				func(context.Context, rfb.PointerEvent) bool { rec.add("never"); return true },
			},
			Clipboard: []ClipboardHandler{
				func(_ context.Context, text string) bool { rec.add("clip " + text); return true },
			},
			Closed: func(err error) { closed <- err },
		}
	}})
	c := dial(t, srv)
	c.key(true, 'a')
	c.key(true, 'b')
	c.pointer(rfb.ButtonLeft, 1, 2)
	// Classic cut text is Latin-1: 0xe9 is é, CRLF becomes LF.
	c.cutText([]byte("caf\xe9\r\nok"), false)
	want := []string{"key1", "key2", "key1", "key2", "key3", "pointer 1", "clip café\nok"}
	waitFor(t, "every hook", func() bool { return len(rec.get()) == len(want) })
	eq(t, want, rec.get())

	_ = c.conn.Close()
	select {
	case err := <-closed:
		if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("closed with %v", err)
		}
	case <-time.After(testWait):
		t.Fatal("Closed hook never ran")
	}
	waitFor(t, "the session to go", func() bool { return srv.NumSessions() == 0 })
}

func TestHooksRunWithoutAConnection(t *testing.T) {
	var h Hooks
	eq(t, false, h.HandleKey(context.Background(), rfb.KeyEvent{}))
	eq(t, false, h.HandlePointer(context.Background(), rfb.PointerEvent{}))
	eq(t, false, h.HandleClipboard(context.Background(), "x"))
	h.Clipboard = []ClipboardHandler{func(context.Context, string) bool { return true }}
	eq(t, true, h.HandleClipboard(context.Background(), "x"))
}

// overlayAt draws a w x h block of c at (x, y) and records the framebuffer it was drawn for.
func overlayAt(x, y, w, h int, c color.RGBA, fbs chan<- image.Rectangle) Overlay {
	return func(fb image.Rectangle) image.Image {
		if fbs != nil {
			fbs <- fb
		}
		img := image.NewRGBA(image.Rect(x, y, x+w, y+h))
		for i := range img.Pix {
			img.Pix[i] = []byte{c.R, c.G, c.B, c.A}[i%4]
		}
		return img
	}
}

func TestOverlaysRideOnTopAndRemovalRepaints(t *testing.T) {
	sessions := make(chan *Session, 1)
	srv := newTestServer(t, Options{OnSession: func(s *Session) Hooks { sessions <- s; return Hooks{} }})
	srv.Publish(Frame{JPEG: testJPEG(t, 64, 48, color.RGBA{A: 255})})
	c := dial(t, srv, rfb.EncodingTight, rfb.EncodingRaw)
	sess := <-sessions
	c.request(false)
	eq(t, []int32{rfb.EncodingTight}, encodings(c.readUpdate()))

	// On a static page the overlay alone answers the open request, at its position.
	fbs := make(chan image.Rectangle, 4)
	c.request(true)
	eq(t, int32(rfb.EncodingRaw), sess.OverlayEncoding())
	sess.SetOverlays(overlayAt(50, 40, 20, 4, color.RGBA{R: 1, G: 2, B: 3, A: 255}, fbs))
	rects := c.readUpdate()
	eq(t, []int32{rfb.EncodingRaw}, encodings(rects))
	eq(t, image.Rect(0, 0, 64, 48), <-fbs)
	// Clipped to the framebuffer.
	eq(t, [4]int{50, 40, 14, 4}, [4]int{rects[0].x, rects[0].y, rects[0].w, rects[0].h})
	eq(t, []byte{3, 2, 1, 0}, rects[0].data[:4])

	// A new frame repaints the page, so the overlay is sent again after it, not redrawn.
	c.request(true)
	srv.Publish(Frame{JPEG: testJPEG(t, 64, 48, color.RGBA{G: 255, A: 255})})
	rects = c.readUpdate()
	eq(t, []int32{rfb.EncodingTight, rfb.EncodingRaw}, encodings(rects))
	eq(t, 40, rects[1].y)
	eq(t, 0, len(fbs))

	// Removing it repaints the whole page with nothing on top.
	c.request(true)
	sess.SetOverlays()
	eq(t, []int32{rfb.EncodingTight}, encodings(c.readUpdate()))
}

func TestOverlaysAsRREForAClientThatPrefersIt(t *testing.T) {
	sessions := make(chan *Session, 1)
	srv := newTestServer(t, Options{OnSession: func(s *Session) Hooks { sessions <- s; return Hooks{} }})
	srv.Publish(Frame{JPEG: testJPEG(t, 64, 48, color.RGBA{A: 255})})
	// noVNC's order: Tight and RRE before Raw.
	c := dial(t, srv, rfb.EncodingTight, rfb.EncodingRRE, rfb.EncodingRaw, rfb.EncodingDesktopSize)
	sess := <-sessions
	c.request(false)
	c.readUpdate()
	waitFor(t, "the encodings", func() bool { return sess.OverlayEncoding() == rfb.EncodingRRE })
	eq(t, []int32{rfb.EncodingTight, rfb.EncodingRRE, rfb.EncodingRaw, rfb.EncodingDesktopSize}, sess.Encodings())
	c.request(true)
	sess.SetOverlays(overlayAt(5, 6, 8, 8, color.RGBA{R: 0x29, G: 0x2a, B: 0x2d, A: 255}, nil))
	rects := c.readUpdate()
	eq(t, []int32{rfb.EncodingRRE}, encodings(rects))
	eq(t, []byte{0x2d, 0x2a, 0x29, 0}, rects[0].data[4:8])
}

func TestOverlayIsRedrawnForANewFramebufferSize(t *testing.T) {
	sessions := make(chan *Session, 1)
	srv := newTestServer(t, Options{OnSession: func(s *Session) Hooks { sessions <- s; return Hooks{} }})
	srv.Publish(Frame{JPEG: testJPEG(t, 64, 48, color.RGBA{A: 255})})
	c := dial(t, srv, rfb.EncodingTight, rfb.EncodingRaw, rfb.EncodingDesktopSize)
	sess := <-sessions
	c.request(false)
	c.readUpdate()
	fbs := make(chan image.Rectangle, 4)
	c.request(true)
	sess.SetOverlays(overlayAt(0, 0, 4, 4, color.RGBA{A: 255}, fbs))
	c.readUpdate()
	eq(t, image.Rect(0, 0, 64, 48), <-fbs)
	c.request(true)
	srv.Publish(Frame{JPEG: testJPEG(t, 32, 24, color.RGBA{A: 255})})
	eq(t, []int32{rfb.EncodingDesktopSize, rfb.EncodingTight, rfb.EncodingRaw}, encodings(c.readUpdate()))
	eq(t, image.Rect(0, 0, 32, 24), <-fbs)
}

// A pass that finds nothing to send keeps the viewer's request: consumed, noVNC would never ask
// again and its view would freeze.
func TestEmptyPassKeepsTheRequest(t *testing.T) {
	sessions := make(chan *Session, 1)
	srv := newTestServer(t, Options{OnSession: func(s *Session) Hooks { sessions <- s; return Hooks{} }})
	srv.Publish(Frame{JPEG: testJPEG(t, 64, 48, color.RGBA{A: 255})})
	c := dial(t, srv, rfb.EncodingTight)
	sess := <-sessions
	c.request(false)
	c.readUpdate()
	c.request(true)
	sess.SetOverlays(func(image.Rectangle) image.Image { return nil })
	c.expectSilence(100 * time.Millisecond)
	sess.Refresh()
	eq(t, []int32{rfb.EncodingTight}, encodings(c.readUpdate()))
}

func TestServerRefreshRepaintsEveryViewer(t *testing.T) {
	srv := newTestServer(t, Options{})
	srv.Publish(Frame{JPEG: testJPEG(t, 64, 48, color.RGBA{A: 255})})
	a := dial(t, srv, rfb.EncodingTight)
	b := dial(t, srv, rfb.EncodingTight)
	for _, c := range []*client{a, b} {
		c.request(false)
		c.readUpdate()
		c.request(true)
	}
	waitFor(t, "both viewers", func() bool { return srv.NumSessions() == 2 })
	srv.Refresh()
	for _, c := range []*client{a, b} {
		eq(t, []int32{rfb.EncodingTight}, encodings(c.readUpdate()))
	}
	eq(t, 2, len(srv.Sessions()))
}

func TestSetClipboardReachesEveryClassicViewerAsLatin1(t *testing.T) {
	srv := newTestServer(t, Options{})
	a := dial(t, srv, rfb.EncodingTight)
	b := dial(t, srv, rfb.EncodingTight)
	waitFor(t, "both viewers", func() bool { return srv.NumSessions() == 2 })
	srv.SetClipboard("naïve ☃")
	for _, c := range []*client{a, b} {
		got, ext := c.readCutText()
		eq(t, false, ext)
		eq(t, []byte("na\xefve ?"), got)
	}
}

func TestSendClipboardReachesOneViewer(t *testing.T) {
	sessions := make(chan *Session, 2)
	srv := newTestServer(t, Options{OnSession: func(s *Session) Hooks { sessions <- s; return Hooks{} }})
	a := dial(t, srv, rfb.EncodingTight)
	b := dial(t, srv, rfb.EncodingTight)
	first := <-sessions
	<-sessions
	eq(t, true, first.SendClipboard("only"))
	// Whichever viewer that session is, exactly one of them gets it.
	got := 0
	for _, c := range []*client{a, b} {
		noErr(t, c.conn.SetReadDeadline(time.Now().Add(200*time.Millisecond)))
		hdr := make([]byte, 1)
		if _, err := c.conn.Read(hdr); err == nil {
			got++
			eq(t, rfb.MsgServerCutText, hdr[0])
			c.read(7)
			eq(t, []byte("only"), c.read(4))
		}
		noErr(t, c.conn.SetReadDeadline(time.Now().Add(testWait)))
	}
	eq(t, 1, got)
}

func TestExtendedClipboard(t *testing.T) {
	texts := make(chan string, 4)
	srv := newTestServer(t, Options{OnSession: func(*Session) Hooks {
		return Hooks{Clipboard: []ClipboardHandler{func(_ context.Context, text string) bool { texts <- text; return true }}}
	}})
	c := dial(t, srv, rfb.EncodingTight, rfb.EncodingExtendedClipboard)

	// SetEncodings with the pseudo-encoding gets the caps: caps|request|peek|notify|provide, text,
	// unsolicited text up to 1 MiB.
	payload, ext := c.readCutText()
	eq(t, true, ext)
	eq(t, []byte{0x1f, 0, 0, 0x01, 0x00, 0x10, 0x00, 0x00}, payload)

	// Peek with nothing on the server clipboard: notify with no formats.
	c.cutText(rfb.ExtendedAction(rfb.ClipPeek, 0), true)
	eq(t, rfb.ClipNotify, c.readExtended().Flags)

	// The viewer notifies, the server requests, the viewer provides UTF-8.
	c.cutText(rfb.ExtendedAction(rfb.ClipNotify, rfb.ClipText), true)
	eq(t, rfb.ClipRequest|rfb.ClipText, c.readExtended().Flags)
	provide, err := rfb.ExtendedProvideText("héllo ☃\nline")
	noErr(t, err)
	c.cutText(provide, true)
	eq(t, "héllo ☃\nline", <-texts)

	// With the default client caps the server provides straight away.
	srv.SetClipboard("ünï ☃")
	e := c.readExtended()
	eq(t, rfb.ClipProvide|rfb.ClipText, e.Flags)
	text, ok, err := e.ProvideText(rfb.MaxCutText)
	noErr(t, err)
	eq(t, true, ok)
	eq(t, "ünï ☃", text)

	// noVNC's caps say 0 bytes unsolicited: notify, then the request gets provide.
	c.cutText([]byte{0x1f, 0, 0, 0x01, 0, 0, 0, 0}, true)
	srv.SetClipboard("second")
	eq(t, rfb.ClipNotify|rfb.ClipText, c.readExtended().Flags)
	c.cutText(rfb.ExtendedAction(rfb.ClipRequest, rfb.ClipText), true)
	text, _, err = c.readExtended().ProvideText(rfb.MaxCutText)
	noErr(t, err)
	eq(t, "second", text)

	// Peek is answered with notify for text now.
	c.cutText(rfb.ExtendedAction(rfb.ClipPeek, 0), true)
	eq(t, rfb.ClipNotify|rfb.ClipText, c.readExtended().Flags)

	// A malformed payload and a provide over 1 MiB are dropped; the viewer stays.
	c.cutText([]byte{1}, true)
	c.cutText(zlibProvide(t, strings.Repeat("a", rfb.MaxCutText+1)), true)
	c.cutText(provide, true)
	eq(t, "héllo ☃\nline", <-texts)
}

func TestExtendedClipboardWithdrawnGoesBackToClassic(t *testing.T) {
	srv := newTestServer(t, Options{})
	c := dial(t, srv, rfb.EncodingTight, rfb.EncodingExtendedClipboard)
	c.readCutText()
	c.setEncodings(rfb.EncodingTight)
	c.request(false) // a round trip: the SetEncodings above has been read
	srv.Publish(Frame{JPEG: testJPEG(t, 8, 8, color.RGBA{A: 255})})
	c.readUpdate()
	srv.SetClipboard("ü☃")
	got, ext := c.readCutText()
	eq(t, false, ext)
	eq(t, []byte{0xfc, '?'}, got)
}

func TestLateViewerCanRequestTheServerClipboard(t *testing.T) {
	srv := newTestServer(t, Options{})
	srv.SetClipboard("earlier")
	c := dial(t, srv, rfb.EncodingExtendedClipboard)
	c.readCutText()
	c.cutText(rfb.ExtendedAction(rfb.ClipRequest, rfb.ClipText), true)
	text, _, err := c.readExtended().ProvideText(rfb.MaxCutText)
	noErr(t, err)
	eq(t, "earlier", text)
}

func TestCursorOncePerSetEncodingsAndPixelFormat(t *testing.T) {
	arrow := solid(3, 2, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	arrow.SetRGBA(2, 1, color.RGBA{})
	srv := newTestServer(t, Options{Cursor: &Cursor{Image: arrow, HotX: 1, HotY: 1}})
	srv.Publish(Frame{JPEG: testJPEG(t, 32, 16, color.RGBA{A: 255})})
	c := dial(t, srv, rfb.EncodingTight, rfb.EncodingCursor)

	c.request(false)
	rects := c.readUpdate()
	eq(t, []int32{rfb.EncodingTight, rfb.EncodingCursor}, encodings(rects))
	cur := rects[1]
	eq(t, [4]int{1, 1, 3, 2}, [4]int{cur.x, cur.y, cur.w, cur.h})
	eq(t, []byte{0xe0, 0xc0}, cur.mask)

	c.request(false)
	eq(t, []int32{rfb.EncodingTight}, encodings(c.readUpdate()))

	c.setPixelFormat([]byte{16, 16, 0, 1, 0, 31, 0, 63, 0, 31, 11, 5, 0, 0, 0, 0})
	c.request(true)
	rects = c.readUpdate()
	eq(t, []int32{rfb.EncodingTight, rfb.EncodingCursor}, encodings(rects))
	eq(t, 3*2*2, len(rects[1].data))
	eq(t, []byte{0xff, 0xff}, rects[1].data[:2])

	c.setEncodings(rfb.EncodingTight)
	c.request(false)
	eq(t, []int32{rfb.EncodingTight}, encodings(c.readUpdate()))
}

func TestColourMapFormatClosesTheSession(t *testing.T) {
	srv := newTestServer(t, Options{})
	conn, done := serveConn(t, srv)
	c := &client{t: t, conn: conn, bpp: 32}
	c.handshakeNone()
	c.setPixelFormat([]byte{8, 8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want the pixel format error")
		}
	case <-time.After(testWait):
		t.Fatal("session stayed open")
	}
}

func TestVNCAuth(t *testing.T) {
	for _, tt := range []struct {
		name     string
		password string
		ok       bool
	}{{"right password", "secret", true}, {"wrong password", "guess", false}} {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t, Options{Security: []rfb.SecurityType{rfb.VNCAuth{Password: "secret"}}})
			conn, done := serveConn(t, srv)
			c := &client{t: t, conn: conn, bpp: 32}
			c.read(rfb.VersionLen)
			c.write([]byte(rfb.Version38))
			eq(t, []byte{1, rfb.SecurityVNCAuth}, c.read(2))
			c.write([]byte{rfb.SecurityVNCAuth})
			var challenge [rfb.VNCAuthChallengeLen]byte
			copy(challenge[:], c.read(rfb.VNCAuthChallengeLen))
			resp, err := rfb.VNCAuthResponse(tt.password, challenge)
			noErr(t, err)
			c.write(resp[:])
			result := binary.BigEndian.Uint32(c.read(4))
			if !tt.ok {
				eq(t, rfb.SecurityResultBad, result)
				c.read(int(binary.BigEndian.Uint32(c.read(4))))
				if err := <-done; !errors.Is(err, rfb.ErrAuthFailed) {
					t.Fatalf("ServeConn returned %v", err)
				}
				return
			}
			eq(t, rfb.SecurityResultOK, result)
			c.clientInit()
			waitFor(t, "the session", func() bool {
				s := srv.Sessions()
				return len(s) == 1 && s[0].SecurityType() == rfb.SecurityVNCAuth
			})
		})
	}
}

func TestMaxSessionsCountsHandshakes(t *testing.T) {
	srv := newTestServer(t, Options{MaxSessions: 1})
	first, _ := serveConn(t, srv) // still in the handshake
	first.Read(make([]byte, rfb.VersionLen))
	_, done := serveConn(t, srv)
	select {
	case err := <-done:
		if !errors.Is(err, ErrTooManySessions) {
			t.Fatalf("second connection: %v", err)
		}
	case <-time.After(testWait):
		t.Fatal("the second connection was not refused")
	}
}

// noDeadlines hides a net.Conn's deadlines, like a transport that has none.
type noDeadlines struct{ c net.Conn }

func (n noDeadlines) Read(p []byte) (int, error)  { return n.c.Read(p) }
func (n noDeadlines) Write(p []byte) (int, error) { return n.c.Write(p) }
func (n noDeadlines) Close() error                { return n.c.Close() }

func TestHandshakeTimeoutWithoutDeadlines(t *testing.T) {
	srv := newTestServer(t, Options{HandshakeTimeout: 50 * time.Millisecond})
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan error, 1)
	go func() { done <- srv.ServeConn(context.Background(), noDeadlines{server}) }()
	client.Read(make([]byte, rfb.VersionLen)) // then say nothing
	select {
	case err := <-done:
		if !errors.Is(err, errHandshakeTimeout) {
			t.Fatalf("err %v", err)
		}
	case <-time.After(testWait):
		t.Fatal("the handshake never timed out")
	}
}

func TestWriteTimeoutClosesAViewerThatStopsReading(t *testing.T) {
	srv := newTestServer(t, Options{WriteTimeout: 50 * time.Millisecond})
	srv.Publish(Frame{JPEG: testJPEG(t, 64, 48, color.RGBA{A: 255})})
	server, conn := net.Pipe()
	defer conn.Close()
	done := make(chan error, 1)
	go func() { done <- srv.ServeConn(context.Background(), noDeadlines{server}) }()
	c := &client{t: t, conn: conn, bpp: 32}
	noErr(t, conn.SetDeadline(time.Now().Add(testWait)))
	c.handshakeNone()
	c.request(false) // and never read the update
	select {
	case <-done:
	case <-time.After(testWait):
		t.Fatal("a viewer that does not read was never closed")
	}
}

func TestServeStopsCleanlyOnCancel(t *testing.T) {
	before := runtime.NumGoroutine()
	srv := newTestServer(t, Options{})
	srv.Publish(Frame{JPEG: testJPEG(t, 16, 16, color.RGBA{A: 255})})
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	noErr(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx, ln) }()

	var conns []net.Conn
	for range 2 {
		var d net.Dialer
		conn, err := d.DialContext(context.Background(), "tcp", ln.Addr().String())
		noErr(t, err)
		conns = append(conns, conn)
		noErr(t, conn.SetDeadline(time.Now().Add(testWait)))
		c := &client{t: t, conn: conn, bpp: 32}
		c.handshakeNone()
		c.setEncodings(rfb.EncodingTight)
		c.request(false)
		c.readUpdate()
	}
	waitFor(t, "two sessions", func() bool { return srv.NumSessions() == 2 })
	cancel()
	select {
	case err := <-served:
		noErr(t, err)
	case <-time.After(testWait):
		t.Fatal("Serve did not return after cancel")
	}
	eq(t, 0, srv.NumSessions())
	for _, conn := range conns {
		if _, err := conn.Read(make([]byte, 1)); err == nil {
			t.Fatal("a viewer is still connected after Serve returned")
		}
		_ = conn.Close()
	}
	waitFor(t, "no leaked goroutines", func() bool { return runtime.NumGoroutine() <= before })
}

func TestServeReturnsWhenTheListenerIsClosed(t *testing.T) {
	srv := newTestServer(t, Options{})
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	noErr(t, err)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(context.Background(), ln) }()
	_ = ln.Close()
	select {
	case err := <-served:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("err %v", err)
		}
	case <-time.After(testWait):
		t.Fatal("Serve did not return")
	}
}

func TestPrefersRRE(t *testing.T) {
	tests := []struct {
		name string
		encs []int32
		want bool
	}{
		{"noVNC order", []int32{rfb.EncodingTight, rfb.EncodingRRE, rfb.EncodingRaw}, true},
		{"raw before rre", []int32{rfb.EncodingRaw, rfb.EncodingRRE}, false},
		{"no rre", []int32{rfb.EncodingTight, rfb.EncodingRaw}, false},
		{"rre only", []int32{rfb.EncodingRRE}, true},
		{"none", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { eq(t, tt.want, prefersRRE(tt.encs)) })
	}
}

func TestSessionAccessors(t *testing.T) {
	sessions := make(chan *Session, 1)
	srv := newTestServer(t, Options{OnSession: func(s *Session) Hooks { sessions <- s; return Hooks{} }})
	dial(t, srv)
	s := <-sessions
	eq(t, "pipe", s.RemoteAddr())
	eq(t, rfb.DefaultPixelFormat(), s.PixelFormat())
	eq(t, []int32(nil), s.Encodings())
	if s.Context().Err() != nil {
		t.Fatal("a live session's context is done")
	}
	noErr(t, s.Close())
	waitFor(t, "the context to end", func() bool { return s.Context().Err() != nil })
}
