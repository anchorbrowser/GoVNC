package govnc

import (
	"errors"
	"fmt"
	"image"
	"log/slog"
	"time"

	"github.com/anchorbrowser/govnc/rfb"
)

// Defaults for zero Options fields.
const (
	DefaultName             = "GoVNC"
	DefaultHandshakeTimeout = 10 * time.Second
	DefaultWriteTimeout     = 10 * time.Second
)

// Options configure a Server. The zero value is a working server: security None, no viewer
// limit, a 1x1 framebuffer until the first Publish.
type Options struct {
	// Name is the desktop name sent in ServerInit; empty is DefaultName.
	Name string
	// Width and Height are the framebuffer size until the first Publish.
	Width, Height int
	// MaxSessions bounds open connections, handshakes included; 0 is no limit. A connection over
	// the limit is closed before the handshake.
	MaxSessions int
	// HandshakeTimeout bounds the handshake; 0 is DefaultHandshakeTimeout.
	HandshakeTimeout time.Duration
	// WriteTimeout bounds every write to a viewer; 0 is DefaultWriteTimeout. A viewer that does
	// not read for that long is closed.
	WriteTimeout time.Duration
	// Security is offered in order; empty offers rfb.NoSecurity only.
	Security []rfb.SecurityType
	// Cursor is sent to viewers that take the Cursor pseudo-encoding, once per SetEncodings and
	// pixel format; nil sends none.
	Cursor *Cursor
	// OnSession is called once per viewer after the handshake, before any of its messages is
	// read. The returned Hooks receive that viewer's input.
	OnSession func(*Session) Hooks
	// Logger gets the server's own lines; nil discards them.
	Logger *slog.Logger
}

// Cursor is a pointer image the viewer draws itself at the hotspot.
type Cursor struct {
	Image      *image.RGBA
	HotX, HotY int
}

func (o Options) withDefaults() (Options, error) {
	if o.Name == "" {
		o.Name = DefaultName
	}
	if o.HandshakeTimeout <= 0 {
		o.HandshakeTimeout = DefaultHandshakeTimeout
	}
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = DefaultWriteTimeout
	}
	if o.MaxSessions < 0 {
		return o, errors.New("govnc: MaxSessions must not be negative")
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	for i, s := range o.Security {
		if s == nil {
			return o, fmt.Errorf("govnc: security type %d is nil", i)
		}
		if v, ok := s.(rfb.VNCAuth); ok && v.Password == "" {
			return o, errors.New("govnc: VNCAuth needs a password")
		}
		if v, ok := s.(*rfb.VNCAuth); ok && (v == nil || v.Password == "") {
			return o, errors.New("govnc: VNCAuth needs a password")
		}
	}
	if c := o.Cursor; c != nil {
		if c.Image == nil {
			return o, errors.New("govnc: cursor has no image")
		}
		if _, err := rfb.CursorRect(c.Image, c.HotX, c.HotY, rfb.DefaultPixelFormat()); err != nil {
			return o, fmt.Errorf("govnc: %w", err)
		}
	}
	return o, nil
}
