package govnc

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/anchorbrowser/govnc/rfb"
)

const testWait = 5 * time.Second

func eq(t *testing.T, want, got any) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("want %#v, got %#v", want, got)
	}
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// waitFor polls cond until it holds or testWait passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(testWait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)
	return img
}

func testJPEG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	var buf bytes.Buffer
	noErr(t, jpeg.Encode(&buf, solid(w, h, c), &jpeg.Options{Quality: 95}))
	return buf.Bytes()
}

func newTestServer(t *testing.T, opts Options) *Server {
	t.Helper()
	if opts.Width == 0 {
		opts.Width, opts.Height = 64, 48
	}
	if opts.Name == "" {
		opts.Name = "test"
	}
	srv, err := NewServer(opts)
	noErr(t, err)
	return srv
}

// serveConn runs one ServeConn over net.Pipe and returns the client end; the result arrives on
// the returned channel. The server is stopped when the test ends.
func serveConn(t *testing.T, srv *Server) (net.Conn, <-chan error) {
	t.Helper()
	server, client := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() { defer close(finished); done <- srv.ServeConn(ctx, server) }()
	t.Cleanup(func() {
		cancel()
		_ = client.Close()
		select {
		case <-finished:
		case <-time.After(testWait):
			t.Error("ServeConn did not return")
		}
	})
	noErr(t, client.SetDeadline(time.Now().Add(testWait)))
	return client, done
}

// client is a minimal RFB viewer.
type client struct {
	t      *testing.T
	conn   net.Conn
	width  int
	height int
	bpp    int
	name   string
}

// dial connects a 3.8 viewer with security None over a pipe and sends encodings, if any.
func dial(t *testing.T, srv *Server, encodings ...int32) *client {
	t.Helper()
	conn, _ := serveConn(t, srv)
	c := &client{t: t, conn: conn, bpp: 32}
	c.handshakeNone()
	if len(encodings) > 0 {
		c.setEncodings(encodings...)
	}
	return c
}

func (c *client) handshakeNone() {
	c.t.Helper()
	eq(c.t, rfb.Version38, string(c.read(rfb.VersionLen)))
	c.write([]byte(rfb.Version38))
	eq(c.t, []byte{1, rfb.SecurityNone}, c.read(2))
	c.write([]byte{rfb.SecurityNone})
	eq(c.t, []byte{0, 0, 0, 0}, c.read(4))
	c.clientInit()
}

func (c *client) clientInit() {
	c.t.Helper()
	c.write([]byte{1})
	init := c.read(24)
	c.width, c.height = int(binary.BigEndian.Uint16(init)), int(binary.BigEndian.Uint16(init[2:]))
	c.name = string(c.read(int(binary.BigEndian.Uint32(init[20:]))))
}

func (c *client) read(n int) []byte {
	c.t.Helper()
	b := make([]byte, n)
	if _, err := io.ReadFull(c.conn, b); err != nil {
		c.t.Fatalf("client read %d: %v", n, err)
	}
	return b
}

func (c *client) write(b []byte) {
	c.t.Helper()
	if _, err := c.conn.Write(b); err != nil {
		c.t.Fatalf("client write: %v", err)
	}
}

func (c *client) setEncodings(encs ...int32) {
	b := binary.BigEndian.AppendUint16([]byte{rfb.MsgSetEncodings, 0}, uint16(len(encs)))
	for _, e := range encs {
		b = binary.BigEndian.AppendUint32(b, uint32(e))
	}
	c.write(b)
}

func (c *client) setPixelFormat(pf []byte) {
	c.write(append([]byte{rfb.MsgSetPixelFormat, 0, 0, 0}, pf...))
	c.bpp = int(pf[0])
}

func (c *client) request(incremental bool) {
	b := []byte{rfb.MsgFramebufferUpdateRequest, 0}
	if incremental {
		b[1] = 1
	}
	b = binary.BigEndian.AppendUint16(b, 0)
	b = binary.BigEndian.AppendUint16(b, 0)
	b = binary.BigEndian.AppendUint16(b, uint16(c.width))
	b = binary.BigEndian.AppendUint16(b, uint16(c.height))
	c.write(b)
}

func (c *client) key(down bool, ks uint32) {
	b := []byte{rfb.MsgKeyEvent, 0, 0, 0}
	if down {
		b[1] = 1
	}
	c.write(binary.BigEndian.AppendUint32(b, ks))
}

func (c *client) pointer(mask uint8, x, y uint16) {
	b := []byte{rfb.MsgPointerEvent, mask}
	b = binary.BigEndian.AppendUint16(b, x)
	c.write(binary.BigEndian.AppendUint16(b, y))
}

func (c *client) cutText(data []byte, extended bool) {
	n := int32(len(data))
	if extended {
		n = -n
	}
	c.write(append(binary.BigEndian.AppendUint32([]byte{rfb.MsgClientCutText, 0, 0, 0}, uint32(n)), data...))
}

// readCutText reads one ServerCutText and returns its payload and whether it was extended.
func (c *client) readCutText() ([]byte, bool) {
	c.t.Helper()
	hdr := c.read(8)
	if hdr[0] != rfb.MsgServerCutText {
		c.t.Fatalf("message type %d, want ServerCutText", hdr[0])
	}
	n := int32(binary.BigEndian.Uint32(hdr[4:]))
	if n < 0 {
		return c.read(int(-n)), true
	}
	return c.read(int(n)), false
}

func (c *client) readExtended() rfb.ExtendedClipboard {
	c.t.Helper()
	payload, ext := c.readCutText()
	if !ext {
		c.t.Fatal("want an extended ServerCutText")
	}
	e, err := rfb.ParseExtendedClipboard(payload)
	noErr(c.t, err)
	return e
}

type rect struct {
	x, y, w, h int
	encoding   int32
	data       []byte
	// mask is a Cursor rect's mask; data holds its pixels.
	mask []byte
}

// readUpdate reads one FramebufferUpdate and applies DesktopSize to the client.
func (c *client) readUpdate() []rect {
	c.t.Helper()
	hdr := c.read(4)
	if hdr[0] != rfb.MsgFramebufferUpdate {
		c.t.Fatalf("message type %d, want FramebufferUpdate", hdr[0])
	}
	n := int(binary.BigEndian.Uint16(hdr[2:]))
	out := make([]rect, 0, n)
	for i := 0; i < n; i++ {
		rh := c.read(12)
		r := rect{
			x: int(binary.BigEndian.Uint16(rh)), y: int(binary.BigEndian.Uint16(rh[2:])),
			w: int(binary.BigEndian.Uint16(rh[4:])), h: int(binary.BigEndian.Uint16(rh[6:])),
			encoding: int32(binary.BigEndian.Uint32(rh[8:])),
		}
		switch r.encoding {
		case rfb.EncodingDesktopSize:
			c.width, c.height = r.w, r.h
		case rfb.EncodingTight:
			eq(c.t, rfb.TightJPEG, c.read(1)[0])
			b := c.read(1)[0]
			length := int(b & 0x7f)
			if b&0x80 != 0 {
				b = c.read(1)[0]
				length |= int(b&0x7f) << 7
				if b&0x80 != 0 {
					length |= int(c.read(1)[0]) << 14
				}
			}
			r.data = c.read(length)
		case rfb.EncodingRaw:
			r.data = c.read(r.w * r.h * c.bpp / 8)
		case rfb.EncodingRRE:
			head := c.read(4 + c.bpp/8)
			n := int(binary.BigEndian.Uint32(head))
			r.data = append(append(r.data, head...), c.read(n*(c.bpp/8+8))...)
		case rfb.EncodingCursor:
			r.data = c.read(r.w * r.h * c.bpp / 8)
			r.mask = c.read((r.w + 7) / 8 * r.h)
		default:
			c.t.Fatalf("unexpected encoding %d", r.encoding)
		}
		out = append(out, r)
	}
	return out
}

func encodings(rects []rect) []int32 {
	out := make([]int32, len(rects))
	for i, r := range rects {
		out[i] = r.encoding
	}
	return out
}

func (c *client) expectSilence(d time.Duration) {
	c.t.Helper()
	noErr(c.t, c.conn.SetReadDeadline(time.Now().Add(d)))
	var b [1]byte
	_, err := c.conn.Read(b[:])
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		c.t.Fatalf("want no data, got err %v", err)
	}
	noErr(c.t, c.conn.SetReadDeadline(time.Now().Add(testWait)))
}

// zlibProvide is an extended provide of text, built by hand so it can exceed the server's limit.
func zlibProvide(t *testing.T, text string) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.Write(binary.BigEndian.AppendUint32(nil, rfb.ClipProvide|rfb.ClipText))
	zw := zlib.NewWriter(&buf)
	_, err := zw.Write(append(binary.BigEndian.AppendUint32(nil, uint32(len(text))), text...))
	noErr(t, err)
	noErr(t, zw.Close())
	return buf.Bytes()
}
