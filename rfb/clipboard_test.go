package rfb

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestExtendedClipboardEncodingNumber(t *testing.T) {
	enc := EncodingExtendedClipboard
	if uint32(enc) != 0xc0a1e5ce {
		t.Fatalf("encoding %#x, want 0xc0a1e5ce", uint32(enc))
	}
}

func TestLatin1(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []byte
	}{
		{"ascii", "hello", []byte("hello")},
		{"latin-1 kept", "café", []byte{'c', 'a', 'f', 0xe9}},
		{"outside latin-1 is ?", "a☃b€", []byte("a?b?")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EncodeLatin1(tt.text); !bytes.Equal(got, tt.want) {
				t.Fatalf("EncodeLatin1 = %v, want %v", got, tt.want)
			}
		})
	}
	if got := DecodeLatin1([]byte{'c', 'a', 'f', 0xe9}); got != "café" {
		t.Fatalf("DecodeLatin1 = %q", got)
	}
}

func TestWriteServerCutText(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteServerCutText(&buf, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if want := []byte{MsgServerCutText, 0, 0, 0, 0, 0, 0, 2, 'h', 'i'}; !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("got %v, want %v", buf.Bytes(), want)
	}
	buf.Reset()
	if err := WriteExtendedServerCutText(&buf, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if want := []byte{MsgServerCutText, 0, 0, 0, 0xff, 0xff, 0xff, 0xfc, 1, 2, 3, 4}; !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("got %v, want %v", buf.Bytes(), want)
	}
	if err := WriteServerCutText(io.Discard, make([]byte, MaxCutText+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err %v, want ErrTooLarge", err)
	}
}

func TestExtendedCapsAndActions(t *testing.T) {
	got := ExtendedCaps(ClipRequest|ClipPeek|ClipNotify|ClipProvide, MaxCutText)
	// Actions in the top byte (caps 24, request 25, peek 26, notify 27, provide 28), text bit 0.
	want := []byte{0x1f, 0, 0, 0x01, 0x00, 0x10, 0x00, 0x00}
	if !bytes.Equal(got, want) {
		t.Fatalf("caps %x, want %x", got, want)
	}
	if got := ExtendedAction(ClipRequest, ClipText); !bytes.Equal(got, []byte{0x02, 0, 0, 0x01}) {
		t.Fatalf("request %x", got)
	}
	if got := ExtendedAction(ClipNotify, 0); !bytes.Equal(got, []byte{0x08, 0, 0, 0}) {
		t.Fatalf("empty notify %x", got)
	}

	// noVNC's caps reply: caps|request|peek|notify|provide, text, size 0.
	e, err := ParseExtendedClipboard([]byte{0x1f, 0, 0, 0x01, 0, 0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	if e.Actions() != ClipCaps|ClipRequest|ClipPeek|ClipNotify|ClipProvide || e.Formats() != ClipText {
		t.Fatalf("flags %#x", e.Flags)
	}
	if limit, ok := e.CapsTextMax(); !ok || limit != 0 {
		t.Fatalf("text max %d %v", limit, ok)
	}
	if _, err := ParseExtendedClipboard([]byte{1, 2}); !errors.Is(err, ErrBadClipboard) {
		t.Fatalf("short payload err %v", err)
	}
}

func TestExtendedProvideTextRoundTrip(t *testing.T) {
	payload, err := ExtendedProvideText("héllo\nwörld ☃")
	if err != nil {
		t.Fatal(err)
	}
	if flags := binary.BigEndian.Uint32(payload); flags != ClipProvide|ClipText {
		t.Fatalf("flags %#x", flags)
	}
	// The zlib stream is size, then UTF-8 with CRLF and a NUL, exactly as noVNC writes it.
	zr, err := zlib.NewReader(bytes.NewReader(payload[4:]))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	text := "héllo\r\nwörld ☃\x00"
	want := append(binary.BigEndian.AppendUint32(nil, uint32(len(text))), text...)
	if !bytes.Equal(raw, want) {
		t.Fatalf("inflated %q, want %q", raw, want)
	}

	e, err := ParseExtendedClipboard(payload)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := e.ProvideText(MaxCutText)
	if err != nil || !ok || got != "héllo\nwörld ☃" {
		t.Fatalf("ProvideText = %q %v %v", got, ok, err)
	}
}

// provide builds a provide payload with one size+data pair per format bit.
func provide(t *testing.T, formats uint32, chunks ...[]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.Write(binary.BigEndian.AppendUint32(nil, ClipProvide|formats))
	zw := zlib.NewWriter(&buf)
	for _, c := range chunks {
		if _, err := zw.Write(append(binary.BigEndian.AppendUint32(nil, uint32(len(c))), c...)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtendedProvideTextFormatsAndLimits(t *testing.T) {
	const rtf, html = 1 << 1, 1 << 2
	tests := []struct {
		name    string
		payload []byte
		limit   int
		want    string
		ok      bool
		err     error
	}{
		{"text with rtf and html after it", provide(t, ClipText|rtf|html, []byte("hi\x00"), []byte("{rtf}"), []byte("<b>")), MaxCutText, "hi", true, nil},
		{"no text format", provide(t, html, []byte("<b>")), MaxCutText, "", false, nil},
		{"decompressed size over the cap", provide(t, ClipText, []byte(strings.Repeat("a", MaxCutText))), MaxCutText, "", false, ErrTooLarge},
		{"truncated stream", provide(t, ClipText|rtf, []byte("hi\x00")), MaxCutText, "", false, ErrBadClipboard},
		{"not zlib", append(binary.BigEndian.AppendUint32(nil, ClipProvide|ClipText), 1, 2, 3), MaxCutText, "", false, ErrBadClipboard},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := ParseExtendedClipboard(tt.payload)
			if err != nil {
				t.Fatal(err)
			}
			got, ok, err := e.ProvideText(tt.limit)
			if !errors.Is(err, tt.err) || ok != tt.ok || got != tt.want {
				t.Fatalf("ProvideText = %q %v %v, want %q %v %v", got, ok, err, tt.want, tt.ok, tt.err)
			}
		})
	}
}
