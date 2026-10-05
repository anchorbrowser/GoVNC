package govnc

import (
	"bufio"
	"context"
	"image"
	"io"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/anchorbrowser/govnc/rfb"
)

const (
	readBuffer = 4 << 10
	// Tight JPEG needs a 16 or 32 bpp client format.
	minTightBPP = 16
)

// Session is one connected viewer. The reader goroutine runs the hooks; the writer goroutine
// owns the connection's write side and the framebuffer size the viewer knows. Its methods are
// safe for concurrent use.
type Session struct {
	srv      *Server
	ctx      context.Context
	conn     io.ReadWriteCloser
	log      *slog.Logger
	version  string
	security uint8
	remote   string
	hooks    Hooks
	wake     chan struct{}
	clip     sessionClipboard

	mu             sync.Mutex
	pf             rfb.PixelFormat
	encodings      []int32
	tight          bool
	rre            bool
	desktopSize    bool
	pending        bool
	full           bool
	cursorOK       bool
	cursorDirty    bool
	overlays       []Overlay
	overlayGen     uint64
	sentOverlayGen uint64
	sentSeq        uint64

	// Writer goroutine only: the framebuffer size the viewer knows and the drawn overlays.
	width, height int
	ov            overlayCache
}

// overlayCache is the overlays as last drawn and encoded. Writer goroutine only.
type overlayCache struct {
	gen    uint64
	fb     image.Rectangle
	images []image.Image
	rects  []rfb.Rect
	pf     rfb.PixelFormat
	enc    int32
}

func newSession(ctx context.Context, srv *Server, conn io.ReadWriteCloser, log *slog.Logger, hs rfb.Handshake, remote string, w, h int) *Session {
	return &Session{
		srv: srv, ctx: ctx, conn: conn, log: log, version: hs.Version, security: hs.Security, remote: remote,
		wake: make(chan struct{}, 1), clip: sessionClipboard{log: log},
		pf: rfb.DefaultPixelFormat(), width: w, height: h,
	}
}

// Context ends when the session does.
func (s *Session) Context() context.Context { return s.ctx }

// RemoteAddr is the viewer's address when the transport has one, else "".
func (s *Session) RemoteAddr() string { return s.remote }

// Version is the protocol version spoken: rfb.Version33, rfb.Version37 or rfb.Version38.
func (s *Session) Version() string { return s.version }

// SecurityType is the security type the viewer passed.
func (s *Session) SecurityType() uint8 { return s.security }

// PixelFormat is the viewer's current pixel format.
func (s *Session) PixelFormat() rfb.PixelFormat {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pf
}

// Encodings is the viewer's last SetEncodings, in its preference order.
func (s *Session) Encodings() []int32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.encodings)
}

// OverlayEncoding is how this viewer gets overlays: RRE when it lists RRE before Raw, else Raw.
func (s *Session) OverlayEncoding() int32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rre {
		return rfb.EncodingRRE
	}
	return rfb.EncodingRaw
}

// SendClipboard sends text to this viewer only, in the form its clipboard support takes, and
// makes it the answer to the viewer's requests. It reports whether anything was queued.
func (s *Session) SendClipboard(text string) bool {
	if !s.clip.offer(text) {
		return false
	}
	s.notify()
	return true
}

// SetOverlays replaces the overlays drawn over the frame; none removes them. Replacing visible
// overlays repaints the whole frame, so nothing of the old ones stays on screen.
func (s *Session) SetOverlays(overlays ...Overlay) {
	s.mu.Lock()
	if len(s.overlays) > 0 {
		s.full = true
	}
	s.overlays = slices.Clone(overlays)
	s.overlayGen++
	s.mu.Unlock()
	s.notify()
}

// Refresh makes the next update a full one, sent as soon as the viewer has a request open.
func (s *Session) Refresh() {
	s.mu.Lock()
	s.full = true
	s.mu.Unlock()
	s.notify()
}

// Close ends the session.
func (s *Session) Close() error { return s.conn.Close() }

func (s *Session) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Session) readLoop() error {
	r := bufio.NewReaderSize(s.conn, readBuffer)
	for {
		msg, err := rfb.ReadClientMessage(r)
		if err != nil {
			return err
		}
		switch m := msg.(type) {
		case rfb.SetPixelFormat:
			if err := m.Format.Validate(); err != nil {
				s.log.Warn("govnc: unsupported pixel format, closing", "error", err)
				return err
			}
			s.mu.Lock()
			s.pf = m.Format
			s.full = true // what the viewer holds is in the old format
			s.cursorDirty = s.cursorOK
			s.mu.Unlock()
		case rfb.SetEncodings:
			s.setEncodings(m.Encodings)
		case rfb.FramebufferUpdateRequest:
			s.mu.Lock()
			s.pending = true
			s.full = s.full || !m.Incremental
			s.mu.Unlock()
			s.notify()
		case rfb.PointerEvent:
			s.hooks.HandlePointer(s.ctx, m)
		case rfb.KeyEvent:
			s.hooks.HandleKey(s.ctx, m)
		case rfb.ClientCutText:
			text, ok, replied := s.clip.fromViewer(m)
			if replied {
				s.notify()
			}
			if ok {
				s.hooks.HandleClipboard(s.ctx, text)
			}
		}
	}
}

func (s *Session) setEncodings(encs []int32) {
	cursor := s.srv.opts.Cursor != nil && slices.Contains(encs, rfb.EncodingCursor)
	s.mu.Lock()
	s.encodings = slices.Clone(encs)
	s.tight = slices.Contains(encs, rfb.EncodingTight)
	s.rre = prefersRRE(encs)
	s.desktopSize = slices.Contains(encs, rfb.EncodingDesktopSize)
	s.cursorOK, s.cursorDirty = cursor, cursor
	s.mu.Unlock()
	if s.srv.opts.Cursor != nil && !cursor {
		s.log.Debug("govnc: viewer has no cursor support; it shows no pointer")
	}
	if s.clip.setEncodings(encs) {
		s.notify()
	}
}

func (s *Session) writeLoop() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		}
		if err := s.flushClipboard(); err != nil {
			s.log.Debug("govnc: write failed, closing viewer", "error", err)
			_ = s.conn.Close()
			return
		}
		f, full, ok := s.take()
		if !ok {
			continue
		}
		if err := s.sendFrame(f, full); err != nil {
			s.log.Debug("govnc: write failed, closing viewer", "error", err)
			_ = s.conn.Close()
			return
		}
	}
}

func (s *Session) flushClipboard() error {
	for _, msg := range s.clip.take() {
		if err := s.write(func(w io.Writer) error {
			_, err := w.Write(msg)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

// write runs fn within the write timeout: a write deadline when the transport has one, else a
// timer that closes the connection.
func (s *Session) write(fn func(io.Writer) error) error {
	timeout := s.srv.opts.WriteTimeout
	if d, ok := s.conn.(interface{ SetWriteDeadline(time.Time) error }); ok {
		if err := d.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
			return err
		}
		return fn(s.conn)
	}
	timer := time.AfterFunc(timeout, func() { _ = s.conn.Close() })
	defer timer.Stop()
	return fn(s.conn)
}

// take returns the frame to send when the viewer has a request open and either asked for a full
// update or has not seen the latest frame, cursor or overlays yet.
func (s *Session) take() (*published, bool, bool) {
	f := s.srv.current()
	s.mu.Lock()
	defer s.mu.Unlock()
	if f == nil || !s.pending || (!s.full && f.seq == s.sentSeq && !s.cursorDirty && s.overlayGen == s.sentOverlayGen) {
		return nil, false, false
	}
	full := s.full
	s.pending, s.full = false, false
	return f, full, true
}

// sendFrame sends DesktopSize first when the frame size changed and the viewer can resize, then
// the page (the JPEG verbatim as Tight, or the picture in the viewer's pixel format), then the
// overlays on top, then the cursor. The page is skipped when only overlays or the cursor changed.
func (s *Session) sendFrame(f *published, full bool) error {
	s.mu.Lock()
	pf, tight, canResize, rre := s.pf, s.tight, s.desktopSize, s.rre
	s.mu.Unlock()
	rects := make([]rfb.Rect, 0, 4)
	resized := false
	if (f.width != s.width || f.height != s.height) && canResize {
		rects = append(rects, rfb.DesktopSizeRect(uint16(f.width), uint16(f.height)))
		s.width, s.height, resized = f.width, f.height, true
	}
	pageSent := full || resized || f.seq != s.sentSeq
	if pageSent {
		page, ok, err := s.pageRect(f, pf, tight, rre)
		if err != nil {
			return err
		}
		if ok {
			rects = append(rects, page)
		}
	}
	enc := rfb.EncodingRaw
	if rre {
		enc = rfb.EncodingRRE
	}
	overlays, err := s.overlayRects(pageSent, pf, enc)
	if err != nil {
		return err
	}
	rects = append(rects, overlays...)
	if cursor, ok := s.takeCursor(pf); ok {
		rects = append(rects, cursor)
	}
	if len(rects) == 0 {
		// Nothing to send after all: keep the request open. A client like noVNC asks again only
		// after an update, so a request consumed without one freezes its view.
		s.mu.Lock()
		s.sentSeq, s.pending, s.full = f.seq, true, s.full || full
		s.mu.Unlock()
		return nil
	}
	if err := s.write(func(w io.Writer) error { return rfb.WriteFramebufferUpdate(w, rects...) }); err != nil {
		return err
	}
	s.mu.Lock()
	s.sentSeq = f.seq
	s.mu.Unlock()
	return nil
}

// pageRect is the frame as one rect at the size the viewer knows; ok is false for a frame that
// cannot be decoded (skipped, logged at debug).
func (s *Session) pageRect(f *published, pf rfb.PixelFormat, tight, rre bool) (rfb.Rect, bool, error) {
	if tight && pf.BitsPerPixel >= minTightBPP && f.width == s.width && f.height == s.height && len(f.frame.JPEG) > 0 {
		if r, err := rfb.TightJPEGRect(uint16(f.width), uint16(f.height), f.frame.JPEG); err == nil {
			return r, true, nil
		}
	}
	img, err := f.image()
	if err != nil {
		s.log.Debug("govnc: undecodable frame skipped", "error", err)
		return rfb.Rect{}, false, nil
	}
	w, h := uint16(s.width), uint16(s.height)
	var r rfb.Rect
	if rre && f.frame.JPEG == nil {
		r, err = rfb.RRERect(img, w, h, pf)
	} else {
		r, err = rfb.RawRect(img, w, h, pf)
	}
	return r, err == nil, err
}

// overlayRects draws the overlays when they or the framebuffer size changed, and returns them
// encoded when they were redrawn or the page under them was just sent.
func (s *Session) overlayRects(pageSent bool, pf rfb.PixelFormat, enc int32) ([]rfb.Rect, error) {
	s.mu.Lock()
	overlays, gen := s.overlays, s.overlayGen
	s.sentOverlayGen = gen
	s.mu.Unlock()
	c := &s.ov
	fb := image.Rect(0, 0, s.width, s.height)
	if gen != c.gen || fb != c.fb {
		c.gen, c.fb, c.images, c.rects = gen, fb, c.images[:0], nil
		for _, o := range overlays {
			if img := o(fb); img != nil {
				c.images = append(c.images, img)
			}
		}
	} else if !pageSent {
		return nil, nil
	}
	if len(c.images) == 0 {
		return nil, nil
	}
	if c.rects == nil || c.pf != pf || c.enc != enc {
		c.rects = make([]rfb.Rect, 0, len(c.images))
		for _, img := range c.images {
			r, ok, err := encodeAt(img, fb, pf, enc)
			if err != nil {
				return nil, err
			}
			if ok {
				c.rects = append(c.rects, r)
			}
		}
		c.pf, c.enc = pf, enc
	}
	return c.rects, nil
}

// takeCursor returns the cursor rect when it is due and clears the flag.
func (s *Session) takeCursor(pf rfb.PixelFormat) (rfb.Rect, bool) {
	s.mu.Lock()
	due := s.cursorDirty
	s.cursorDirty = false
	s.mu.Unlock()
	c := s.srv.opts.Cursor
	if !due || c == nil {
		return rfb.Rect{}, false
	}
	rect, err := rfb.CursorRect(c.Image, c.HotX, c.HotY, pf)
	if err != nil {
		s.log.Debug("govnc: cursor not sent", "error", err)
		return rfb.Rect{}, false
	}
	return rect, true
}
