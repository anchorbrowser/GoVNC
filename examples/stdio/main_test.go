package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

func testJPEG(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 8))
	for i := range img.Pix {
		switch i % 4 {
		case 0:
			img.Pix[i] = c.R
		case 1:
			img.Pix[i] = c.G
		case 2:
			img.Pix[i] = c.B
		default:
			img.Pix[i] = 0xff
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestNextJPEGSplitsAStream(t *testing.T) {
	a := testJPEG(t, color.RGBA{R: 0xff})
	b := testJPEG(t, color.RGBA{B: 0xff})
	stream := append([]byte("junk\xff"), a...)
	stream = append(stream, "more junk"...)
	stream = append(stream, b...)
	r := bufio.NewReader(bytes.NewReader(stream))

	got, err := nextJPEG(r)
	if err != nil || !bytes.Equal(got, a) {
		t.Fatalf("first frame: err %v, equal %v", err, bytes.Equal(got, a))
	}
	got, err = nextJPEG(r)
	if err != nil || !bytes.Equal(got, b) {
		t.Fatalf("second frame: err %v, equal %v", err, bytes.Equal(got, b))
	}
	if _, err := nextJPEG(r); !errors.Is(err, io.EOF) {
		t.Fatalf("after the last frame: want io.EOF, got %v", err)
	}
}

func TestNextJPEGReportsATruncatedFrame(t *testing.T) {
	a := testJPEG(t, color.RGBA{G: 0xff})
	r := bufio.NewReader(bytes.NewReader(a[:len(a)-3]))
	if _, err := nextJPEG(r); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestNextJPEGCapsFrameSize(t *testing.T) {
	endless := io.MultiReader(bytes.NewReader([]byte{0xFF, 0xD8}), zeros{})
	if _, err := nextJPEG(bufio.NewReader(endless)); !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("want errFrameTooLarge, got %v", err)
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func TestEventsAreJSONLines(t *testing.T) {
	var out bytes.Buffer
	w := &eventWriter{enc: json.NewEncoder(&out), log: quiet()}
	if err := w.write(keyEvent{Type: "key", Keysym: 0x68, Down: true}); err != nil {
		t.Fatal(err)
	}
	if err := w.write(pointerEvent{Type: "pointer", X: 10, Y: 20, Buttons: 1}); err != nil {
		t.Fatal(err)
	}
	want := `{"type":"key","keysym":104,"down":true}` + "\n" + `{"type":"pointer","x":10,"y":20,"buttons":1}` + "\n"
	if out.String() != want {
		t.Fatalf("got %q", out.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestEventWriterReportsTheFirstFailure(t *testing.T) {
	var logged bytes.Buffer
	w := &eventWriter{enc: json.NewEncoder(failingWriter{}), log: slog.New(slog.NewTextHandler(&logged, nil))}
	for range 3 {
		if err := w.write(clipboardEvent{Type: "clipboard", Text: "x"}); err == nil {
			t.Fatal("want the write error")
		}
	}
	if n := strings.Count(logged.String(), "not being delivered"); n != 1 {
		t.Fatalf("want one warning, got %d: %s", n, logged.String())
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRunServesTheFirstFrameUntilCancelled(t *testing.T) {
	addr := freeAddr(t)
	in, feed := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, addr, "", in, io.Discard, quiet()) }()
	if _, err := feed.Write(testJPEG(t, color.RGBA{R: 0xff})); err != nil {
		t.Fatal(err)
	}

	var conn net.Conn
	var err error
	deadline := time.Now().Add(5 * time.Second)
	for {
		if conn, err = net.Dial("tcp", addr); err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	banner := make([]byte, 12)
	if _, err := io.ReadFull(conn, banner); err != nil || string(banner) != "RFB 003.008\n" {
		t.Fatalf("banner %q err %v", banner, err)
	}
	_ = conn.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after cancel")
	}
	_ = feed.Close()
}

func TestRunEndsWhenInputEnds(t *testing.T) {
	in := bytes.NewReader(testJPEG(t, color.RGBA{B: 0xff}))
	addr := freeAddr(t)
	done := make(chan error, 1)
	go func() { done <- run(context.Background(), addr, "", in, io.Discard, quiet()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return when stdin ended")
	}
}

func TestRunRejectsInputThatIsNotJPEG(t *testing.T) {
	err := run(context.Background(), freeAddr(t), "", strings.NewReader("not a picture"), io.Discard, quiet())
	if err == nil || !strings.Contains(err.Error(), "first frame") {
		t.Fatalf("want a first-frame error, got %v", err)
	}
}
