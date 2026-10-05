package main

import (
	"context"
	"image/color"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

func TestPatternMoves(t *testing.T) {
	a, b := pattern(64, 8, 0), pattern(64, 8, 1)
	if a.Rect.Dx() != 64 || a.Rect.Dy() != 8 {
		t.Fatalf("pattern is %v", a.Rect)
	}
	white := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	if a.RGBAAt(0, 0) != white || b.RGBAAt(0, 0) == white || b.RGBAAt(8, 0) != white {
		t.Fatal("the white column must move 8 px per frame")
	}
	if _, err := encode(a); err != nil {
		t.Fatal(err)
	}
}

func TestRunServesUntilCancelled(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, addr, "", slog.New(slog.NewTextHandler(io.Discard, nil))) }()

	var conn net.Conn
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
}

func TestRunRejectsAnUnusableAddress(t *testing.T) {
	if err := run(context.Background(), "not an address", "", slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("want a listen error")
	}
}
