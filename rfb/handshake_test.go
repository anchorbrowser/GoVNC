package rfb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func pipe(t *testing.T) (server, client net.Conn) {
	t.Helper()
	server, client = net.Pipe()
	deadline := time.Now().Add(5 * time.Second)
	for _, c := range []net.Conn{server, client} {
		if err := c.SetDeadline(deadline); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	return server, client
}

func mustRead(t *testing.T, r io.Reader, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		t.Fatalf("read %d bytes: %v", n, err)
	}
	return b
}

func mustWrite(t *testing.T, w io.Writer, b []byte) {
	t.Helper()
	if _, err := w.Write(b); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestServerHandshake(t *testing.T) {
	init := ServerInit{Width: 1440, Height: 900, Format: DefaultPixelFormat(), Name: "govnc"}
	wantInit := init.Marshal()
	tests := []struct {
		name        string
		client      string
		wantVersion string
		// security is what the client must read after sending its version.
		security []byte
		// choose is the byte the client sends back (3.7/3.8 only).
		choose []byte
		result []byte
	}{
		{"3.3 server decides None", Version33, Version33, []byte{0, 0, 0, 1}, nil, nil},
		{"3.7 offers None, no result", Version37, Version37, []byte{1, 1}, []byte{1}, nil},
		{"3.8 offers None and sends OK", Version38, Version38, []byte{1, 1}, []byte{1}, []byte{0, 0, 0, 0}},
		{"3.5 is treated as 3.3", "RFB 003.005\n", Version33, []byte{0, 0, 0, 1}, nil, nil},
		{"3.889 speaks 3.8", "RFB 003.889\n", Version38, []byte{1, 1}, []byte{1}, []byte{0, 0, 0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, client := pipe(t)
			type result struct {
				hs  Handshake
				err error
			}
			done := make(chan result, 1)
			go func() {
				hs, err := ServerHandshake(server, init)
				done <- result{hs, err}
			}()
			if got := string(mustRead(t, client, VersionLen)); got != Version38 {
				t.Fatalf("banner %q", got)
			}
			mustWrite(t, client, []byte(tt.client))
			if got := mustRead(t, client, len(tt.security)); !bytes.Equal(got, tt.security) {
				t.Fatalf("security %v, want %v", got, tt.security)
			}
			if tt.choose != nil {
				mustWrite(t, client, tt.choose)
			}
			if tt.result != nil {
				if got := mustRead(t, client, len(tt.result)); !bytes.Equal(got, tt.result) {
					t.Fatalf("result %v", got)
				}
			}
			mustWrite(t, client, []byte{1})
			if got := mustRead(t, client, len(wantInit)); !bytes.Equal(got, wantInit) {
				t.Fatalf("ServerInit %v", got)
			}
			res := <-done
			if res.err != nil || res.hs.Version != tt.wantVersion || !res.hs.Shared {
				t.Fatalf("handshake %+v err %v", res.hs, res.err)
			}
		})
	}
}

func TestServerInitBytes(t *testing.T) {
	got := ServerInit{Width: 0x0102, Height: 0x0304, Format: DefaultPixelFormat(), Name: "ab"}.Marshal()
	want := []byte{
		0x01, 0x02, 0x03, 0x04, // size
		32, 24, 0, 1, // bpp, depth, big-endian, true colour
		0, 255, 0, 255, 0, 255, // maxes
		16, 8, 0, // shifts
		0, 0, 0, // padding
		0, 0, 0, 2, 'a', 'b',
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ServerInit\n got %v\nwant %v", got, want)
	}
}

func TestServerHandshakeRejects(t *testing.T) {
	t.Run("bad version", func(t *testing.T) {
		server, client := pipe(t)
		done := make(chan error, 1)
		go func() { _, err := ServerHandshake(server, ServerInit{}); done <- err }()
		mustRead(t, client, VersionLen)
		mustWrite(t, client, []byte("HTTP/1.1 200"))
		if err := <-done; err == nil {
			t.Fatal("want error for a non-RFB version")
		}
	})
	t.Run("3.8 client picks VNC auth", func(t *testing.T) {
		server, client := pipe(t)
		done := make(chan error, 1)
		go func() { _, err := ServerHandshake(server, ServerInit{}); done <- err }()
		mustRead(t, client, VersionLen)
		mustWrite(t, client, []byte(Version38))
		mustRead(t, client, 2)
		mustWrite(t, client, []byte{2})
		res := mustRead(t, client, 8)
		if binary.BigEndian.Uint32(res) != SecurityResultBad {
			t.Fatalf("result %v", res)
		}
		mustRead(t, client, int(binary.BigEndian.Uint32(res[4:])))
		if err := <-done; !errors.Is(err, ErrSecurityRejected) {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("probe hangs up after the banner", func(t *testing.T) {
		server, client := pipe(t)
		done := make(chan error, 1)
		go func() { _, err := ServerHandshake(server, ServerInit{}); done <- err }()
		mustRead(t, client, VersionLen)
		_ = client.Close()
		if err := <-done; err == nil {
			t.Fatal("want error on early close")
		}
	})
}

func TestPixelFormatValidate(t *testing.T) {
	novnc := PixelFormat{BitsPerPixel: 32, Depth: 24, TrueColour: true, RedMax: 255, GreenMax: 255, BlueMax: 255, RedShift: 0, GreenShift: 8, BlueShift: 16}
	rgb565 := PixelFormat{BitsPerPixel: 16, Depth: 16, TrueColour: true, RedMax: 31, GreenMax: 63, BlueMax: 31, RedShift: 11, GreenShift: 5, BlueShift: 0}
	tests := []struct {
		name string
		pf   PixelFormat
		ok   bool
	}{
		{"default", DefaultPixelFormat(), true},
		{"noVNC rgbx", novnc, true},
		{"rgb565", rgb565, true},
		{"colour map", PixelFormat{BitsPerPixel: 8, Depth: 8}, false},
		{"24 bpp", PixelFormat{BitsPerPixel: 24, Depth: 24, TrueColour: true, RedMax: 255, GreenMax: 255, BlueMax: 255}, false},
		{"channel overflows", PixelFormat{BitsPerPixel: 16, Depth: 16, TrueColour: true, RedMax: 255, GreenMax: 255, BlueMax: 255, RedShift: 10}, false},
		{"zero max", PixelFormat{BitsPerPixel: 32, Depth: 24, TrueColour: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.pf.Validate(); (err == nil) != tt.ok {
				t.Fatalf("Validate = %v, ok want %v", err, tt.ok)
			}
		})
	}
}
