package govnc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/anchorbrowser/govnc/rfb"
)

const acceptBackoff = 100 * time.Millisecond

// ErrTooManySessions is returned by ServeConn when MaxSessions connections are open.
var ErrTooManySessions = errors.New("govnc: too many sessions")

var errHandshakeTimeout = errors.New("govnc: handshake timed out")

// Server is an RFB server: every viewer sees the latest published frame. Its methods are safe
// for concurrent use.
type Server struct {
	opts Options
	log  *slog.Logger

	mu       sync.Mutex
	seq      uint64
	latest   *published
	conns    int
	sessions map[*Session]struct{}
	clipText string
}

// NewServer validates opts and returns a server with no frame yet: viewers are told the Options
// size, and their update requests wait for the first Publish.
func NewServer(opts Options) (*Server, error) {
	opts, err := opts.withDefaults()
	if err != nil {
		return nil, err
	}
	return &Server{opts: opts, log: opts.Logger, sessions: map[*Session]struct{}{}}, nil
}

// Publish replaces the frame every viewer sees and wakes them. It never blocks: a viewer that is
// still writing an older frame gets only the newest one when it is done, and nothing queues. A
// frame of another size resizes the viewers that take DesktopSize; the others get it cropped or
// padded to the size they know.
func (s *Server) Publish(f Frame) {
	s.mu.Lock()
	s.seq++
	s.latest = newPublished(s.seq, f)
	sessions := s.snapshot()
	s.mu.Unlock()
	for _, sess := range sessions {
		sess.notify()
	}
}

// SetClipboard makes text the server clipboard and sends it to every viewer. It also answers the
// requests of viewers that connect later.
func (s *Server) SetClipboard(text string) {
	s.mu.Lock()
	s.clipText = text
	sessions := s.snapshot()
	s.mu.Unlock()
	for _, sess := range sessions {
		sess.SendClipboard(text)
	}
}

// Refresh makes every viewer's next update a full one.
func (s *Server) Refresh() {
	s.mu.Lock()
	sessions := s.snapshot()
	s.mu.Unlock()
	for _, sess := range sessions {
		sess.Refresh()
	}
}

// Sessions returns the viewers past the handshake.
func (s *Server) Sessions() []*Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot()
}

// NumSessions is the number of viewers past the handshake.
func (s *Server) NumSessions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

// snapshot copies the session set; s.mu is held.
func (s *Server) snapshot() []*Session {
	out := make([]*Session, 0, len(s.sessions))
	for sess := range s.sessions {
		out = append(out, sess)
	}
	return out
}

func (s *Server) current() *published {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latest
}

// Serve accepts viewers on ln until ctx ends, then closes ln and returns nil once every session
// it started has ended. An accept error other than a closed listener is logged and retried.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	stop := context.AfterFunc(ctx, func() { _ = ln.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return fmt.Errorf("govnc: serve: %w", err)
			}
			s.log.Warn("govnc: accept", "error", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(acceptBackoff):
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.ServeConn(ctx, conn)
		}()
	}
}

// ServeConn runs one viewer on conn until the viewer leaves, the connection fails or ctx ends,
// and closes conn. It returns nil for a viewer that hung up or a cancelled ctx. Deadlines are used
// when conn has them (a net.Conn); otherwise a timer closes conn on a timeout.
func (s *Server) ServeConn(ctx context.Context, conn io.ReadWriteCloser) error {
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	log := s.log
	remote := remoteAddr(conn)
	if remote != "" {
		log = log.With("remote", remote)
	}
	if !s.reserve() {
		log.Warn("govnc: too many sessions, refusing", "max", s.opts.MaxSessions)
		return ErrTooManySessions
	}
	defer s.release()

	hs, w, h, err := s.handshake(conn)
	if err != nil {
		log.Debug("govnc: handshake ended", "error", err)
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sess := newSession(sctx, s, conn, log, hs, remote, w, h)
	if s.opts.OnSession != nil {
		sess.hooks = s.opts.OnSession(sess)
	}
	s.add(sess)

	writerDone := make(chan struct{})
	go func() { defer close(writerDone); sess.writeLoop() }()
	err = sess.readLoop()
	cancel()
	_ = conn.Close()
	<-writerDone
	s.remove(sess)
	if sess.hooks.Closed != nil {
		sess.hooks.Closed(err)
	}
	if ctx.Err() != nil || errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func (s *Server) reserve() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.opts.MaxSessions > 0 && s.conns >= s.opts.MaxSessions {
		return false
	}
	s.conns++
	return true
}

func (s *Server) release() {
	s.mu.Lock()
	s.conns--
	s.mu.Unlock()
}

// add registers sess and gives it the server clipboard, in one step with SetClipboard's snapshot
// so no text set meanwhile is missed.
func (s *Server) add(sess *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess.clip.setText(s.clipText)
	s.sessions[sess] = struct{}{}
}

func (s *Server) remove(sess *Session) {
	s.mu.Lock()
	delete(s.sessions, sess)
	s.mu.Unlock()
}

// size is the framebuffer size a new viewer is told.
func (s *Server) size() (int, int) {
	if f := s.current(); f != nil {
		return f.width, f.height
	}
	return clampFB(s.opts.Width), clampFB(s.opts.Height)
}

// handshake runs the RFB handshake within the handshake timeout and returns the size announced.
func (s *Server) handshake(conn io.ReadWriteCloser) (rfb.Handshake, int, int, error) {
	w, h := s.size()
	init := rfb.ServerInit{Width: uint16(w), Height: uint16(h), Format: rfb.DefaultPixelFormat(), Name: s.opts.Name}
	if d, ok := conn.(interface{ SetDeadline(time.Time) error }); ok {
		if err := d.SetDeadline(time.Now().Add(s.opts.HandshakeTimeout)); err != nil {
			return rfb.Handshake{}, 0, 0, err
		}
		hs, err := rfb.ServerHandshake(conn, init, s.opts.Security...)
		if err != nil {
			return rfb.Handshake{}, 0, 0, err
		}
		if err := d.SetDeadline(time.Time{}); err != nil {
			return rfb.Handshake{}, 0, 0, err
		}
		return hs, w, h, nil
	}
	timer := time.AfterFunc(s.opts.HandshakeTimeout, func() { _ = conn.Close() })
	hs, err := rfb.ServerHandshake(conn, init, s.opts.Security...)
	if !timer.Stop() {
		return rfb.Handshake{}, 0, 0, errHandshakeTimeout
	}
	if err != nil {
		return rfb.Handshake{}, 0, 0, err
	}
	return hs, w, h, nil
}

func remoteAddr(conn io.ReadWriteCloser) string {
	if c, ok := conn.(interface{ RemoteAddr() net.Addr }); ok && c.RemoteAddr() != nil {
		return c.RemoteAddr().String()
	}
	return ""
}
