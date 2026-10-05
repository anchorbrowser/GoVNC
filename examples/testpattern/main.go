// Command testpattern serves a moving test pattern over VNC. Connect any VNC viewer to the
// address it prints (default 127.0.0.1:5900). Keys and clicks are logged, and clipboard text the
// viewer sends is echoed back to its clipboard.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/anchorbrowser/govnc"
	"github.com/anchorbrowser/govnc/rfb"
)

const (
	width, height = 640, 360
	fps           = 10
)

func main() {
	addr := flag.String("addr", "127.0.0.1:5900", "listen address")
	password := flag.String("password", "", "VNC Authentication password; empty offers security None")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, *addr, *password, log)
	stop()
	if err != nil {
		log.Error("testpattern", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, addr, password string, log *slog.Logger) error {
	opts := govnc.Options{
		Name: "GoVNC test pattern", Width: width, Height: height, MaxSessions: 8, Logger: log,
		OnSession: func(s *govnc.Session) govnc.Hooks { return hooks(s, log) },
	}
	if password != "" {
		opts.Security = []rfb.SecurityType{rfb.VNCAuth{Password: password}}
	}
	srv, err := govnc.NewServer(opts)
	if err != nil {
		return err
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	log.Info("serving", "addr", ln.Addr().String())
	go animate(ctx, srv)
	return srv.Serve(ctx, ln)
}

func hooks(s *govnc.Session, log *slog.Logger) govnc.Hooks {
	log = log.With("remote", s.RemoteAddr())
	log.Info("viewer connected")
	var buttons uint8
	return govnc.Hooks{
		Key: []govnc.KeyHandler{func(_ context.Context, ev rfb.KeyEvent) bool {
			log.Info("key", "keysym", fmt.Sprintf("%#x", ev.Keysym), "down", ev.Down)
			return true
		}},
		Pointer: []govnc.PointerHandler{func(_ context.Context, ev rfb.PointerEvent) bool {
			if pressed := ev.Mask &^ buttons; pressed != 0 {
				log.Info("click", "x", ev.X, "y", ev.Y, "buttons", pressed)
			}
			buttons = ev.Mask
			return true
		}},
		Clipboard: []govnc.ClipboardHandler{func(_ context.Context, text string) bool {
			s.SendClipboard("govnc got: " + text)
			return true
		}},
		Closed: func(err error) { log.Info("viewer disconnected", "reason", err) },
	}
}

// animate publishes a new pattern fps times a second until ctx ends.
func animate(ctx context.Context, srv *govnc.Server) {
	tick := time.NewTicker(time.Second / fps)
	defer tick.Stop()
	for n := 0; ; n++ {
		if jpg, err := encode(pattern(width, height, n)); err == nil {
			srv.Publish(govnc.Frame{JPEG: jpg, Width: width, Height: height})
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

var bars = []color.RGBA{
	{R: 0xc0, G: 0xc0, B: 0xc0, A: 0xff}, {R: 0xc0, G: 0xc0, A: 0xff}, {G: 0xc0, B: 0xc0, A: 0xff},
	{G: 0xc0, A: 0xff}, {R: 0xc0, B: 0xc0, A: 0xff}, {R: 0xc0, A: 0xff}, {B: 0xc0, A: 0xff},
}

// pattern is colour bars with a white column that moves one step per frame n.
func pattern(w, h, n int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	sweep := (n * 8) % w
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := bars[x*len(bars)/w]
			if x >= sweep && x < sweep+8 {
				c = color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func encode(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
