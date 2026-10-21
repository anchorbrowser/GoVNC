// Command stdio makes any process a VNC server. It reads an MJPEG stream (JPEG frames back to
// back, as `ffmpeg -f mjpeg` or a browser screencast produce) on stdin and publishes every frame.
// Viewer input is written to stdout as one JSON object per line, so the process drawing the
// pictures can act on it:
//
//	{"type":"key","keysym":104,"down":true}
//	{"type":"pointer","x":10,"y":20,"buttons":1}
//	{"type":"clipboard","text":"pasted"}
//
// The server exits when stdin ends.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image/jpeg"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"

	"github.com/anchorbrowser/govnc"
	"github.com/anchorbrowser/govnc/rfb"
)

// maxFrame bounds one JPEG so a stream without an End Of Image marker cannot grow memory forever.
const maxFrame = 32 << 20

var errFrameTooLarge = fmt.Errorf("frame exceeds %d bytes", maxFrame)

func main() {
	addr := flag.String("addr", "127.0.0.1:5900", "listen address")
	password := flag.String("password", "", "VNC Authentication password; empty offers security None")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, *addr, *password, os.Stdin, os.Stdout, log)
	stop()
	if err != nil {
		log.Error("stdio", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, addr, password string, in io.Reader, out io.Writer, log *slog.Logger) error {
	frames := bufio.NewReaderSize(in, 1<<20)
	first, err := nextJPEG(frames)
	if err != nil {
		return fmt.Errorf("first frame: %w", err)
	}
	if _, err := jpeg.DecodeConfig(bytes.NewReader(first)); err != nil {
		return fmt.Errorf("first frame: %w", err)
	}
	events := &eventWriter{enc: json.NewEncoder(out), log: log}
	opts := govnc.Options{
		Name: "GoVNC stdio", MaxSessions: 8, Logger: log,
		OnSession: func(s *govnc.Session) govnc.Hooks { return hooks(s, events, log) },
	}
	if password != "" {
		opts.Security = []rfb.SecurityType{rfb.VNCAuth{Password: password}}
	}
	srv, err := govnc.NewServer(opts)
	if err != nil {
		return err
	}
	srv.Publish(govnc.Frame{JPEG: first})
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	log.Info("serving", "addr", ln.Addr().String())

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	published := make(chan error, 1)
	go func() {
		defer cancel()
		published <- publish(frames, srv)
	}()
	if err := srv.Serve(ctx, ln); err != nil {
		return err
	}
	select {
	case err := <-published:
		return err
	default:
		return nil
	}
}

// publish forwards every frame on r until the stream ends.
func publish(r *bufio.Reader, srv *govnc.Server) error {
	for {
		b, err := nextJPEG(r)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read frame: %w", err)
		}
		srv.Publish(govnc.Frame{JPEG: b})
	}
}

// nextJPEG returns the next frame of an MJPEG stream: the bytes from a Start Of Image marker
// (FF D8) to the following End Of Image marker (FF D9). Bytes between frames are skipped. It
// returns io.EOF when the stream ends between frames and io.ErrUnexpectedEOF inside one.
// A JPEG carrying an embedded thumbnail (EXIF) has an inner FF D9 and would be cut short.
func nextJPEG(r *bufio.Reader) ([]byte, error) {
	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if b != 0xFF {
			continue
		}
		next, err := r.Peek(1)
		if err != nil {
			return nil, err
		}
		if next[0] == 0xD8 {
			_, _ = r.ReadByte()
			break
		}
	}
	frame := []byte{0xFF, 0xD8}
	for {
		chunk, err := r.ReadSlice(0xD9)
		frame = append(frame, chunk...)
		if len(frame) > maxFrame {
			return nil, errFrameTooLarge
		}
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return nil, io.ErrUnexpectedEOF
		case err != nil:
			return nil, err
		}
		if frame[len(frame)-2] == 0xFF {
			return frame, nil
		}
	}
}

type keyEvent struct {
	Type   string `json:"type"`
	Keysym uint32 `json:"keysym"`
	Down   bool   `json:"down"`
}

type pointerEvent struct {
	Type    string `json:"type"`
	X       uint16 `json:"x"`
	Y       uint16 `json:"y"`
	Buttons uint8  `json:"buttons"`
}

type clipboardEvent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// eventWriter serialises viewer input onto one stream; hooks of different sessions run concurrently.
type eventWriter struct {
	mu     sync.Mutex
	enc    *json.Encoder
	log    *slog.Logger
	failed bool
}

func (w *eventWriter) write(v any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	err := w.enc.Encode(v)
	if err != nil && !w.failed {
		w.failed = true
		w.log.Warn("input events are not being delivered", "error", err)
	}
	return err
}

func hooks(s *govnc.Session, events *eventWriter, log *slog.Logger) govnc.Hooks {
	log = log.With("remote", s.RemoteAddr())
	log.Info("viewer connected")
	var buttons uint8
	return govnc.Hooks{
		Key: []govnc.KeyHandler{func(_ context.Context, ev rfb.KeyEvent) bool {
			log.Info("key", "keysym", fmt.Sprintf("%#x", ev.Keysym), "down", ev.Down)
			_ = events.write(keyEvent{Type: "key", Keysym: ev.Keysym, Down: ev.Down})
			return true
		}},
		Pointer: []govnc.PointerHandler{func(_ context.Context, ev rfb.PointerEvent) bool {
			if pressed := ev.Mask &^ buttons; pressed != 0 {
				log.Info("click", "x", ev.X, "y", ev.Y, "buttons", pressed)
			}
			buttons = ev.Mask
			_ = events.write(pointerEvent{Type: "pointer", X: ev.X, Y: ev.Y, Buttons: ev.Mask})
			return true
		}},
		Clipboard: []govnc.ClipboardHandler{func(_ context.Context, text string) bool {
			_ = events.write(clipboardEvent{Type: "clipboard", Text: text})
			return true
		}},
		Closed: func(err error) { log.Info("viewer left", "reason", err) },
	}
}
