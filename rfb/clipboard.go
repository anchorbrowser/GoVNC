package rfb

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"strings"
)

// MsgServerCutText is the server clipboard (RFC 6143 7.6.4).
const MsgServerCutText uint8 = 3

// EncodingExtendedClipboard is the Extended Clipboard pseudo-encoding (0xc0a1e5ce, rfbproto).
const EncodingExtendedClipboard int32 = -1063131698

// Extended Clipboard flags: bits 0-15 are formats, bits 24-31 are actions (rfbproto).
const (
	ClipText    uint32 = 1 << 0
	ClipCaps    uint32 = 1 << 24
	ClipRequest uint32 = 1 << 25
	ClipPeek    uint32 = 1 << 26
	ClipNotify  uint32 = 1 << 27
	ClipProvide uint32 = 1 << 28

	ClipFormatMask uint32 = 0x0000ffff
	ClipActionMask uint32 = 0xff000000
)

// ClipDefaultTextMax is the text size a client that sent no caps accepts unsolicited (rfbproto).
const ClipDefaultTextMax = 20 << 20

const (
	cutTextHeaderLen = 8
	clipFlagsLen     = 4
	clipSizeLen      = 4
)

// ErrBadClipboard means an extended clipboard payload is malformed.
var ErrBadClipboard = errors.New("rfb: malformed extended clipboard message")

// DecodeLatin1 decodes classic cut text: every byte is one ISO 8859-1 code point.
func DecodeLatin1(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return sb.String()
}

// EncodeLatin1 encodes text for classic cut text; a code point above 0xff becomes '?'.
func EncodeLatin1(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 0xff {
			r = '?'
		}
		out = append(out, byte(r))
	}
	return out
}

// WriteServerCutText writes classic ServerCutText with Latin-1 text.
func WriteServerCutText(w io.Writer, latin1 []byte) error {
	return writeCutText(w, latin1, false)
}

// WriteExtendedServerCutText writes ServerCutText carrying an extended payload (negative length).
func WriteExtendedServerCutText(w io.Writer, payload []byte) error {
	return writeCutText(w, payload, true)
}

func writeCutText(w io.Writer, data []byte, extended bool) error {
	if len(data) > MaxCutText {
		return fmt.Errorf("%w: cut text of %d bytes", ErrTooLarge, len(data))
	}
	n := int32(len(data))
	if extended {
		n = -n
	}
	b := make([]byte, cutTextHeaderLen, cutTextHeaderLen+len(data))
	b[0] = MsgServerCutText
	binary.BigEndian.PutUint32(b[4:], uint32(n))
	_, err := w.Write(append(b, data...))
	return err
}

// ExtendedClipboard is a decoded extended payload: the flags word and what follows it.
type ExtendedClipboard struct {
	Flags uint32
	Data  []byte
}

// Actions is the action bits of the flags.
func (e ExtendedClipboard) Actions() uint32 { return e.Flags & ClipActionMask }

// Formats is the format bits of the flags.
func (e ExtendedClipboard) Formats() uint32 { return e.Flags & ClipFormatMask }

// ParseExtendedClipboard splits an extended ClientCutText payload.
func ParseExtendedClipboard(payload []byte) (ExtendedClipboard, error) {
	if len(payload) < clipFlagsLen {
		return ExtendedClipboard{}, fmt.Errorf("%w: %d bytes", ErrBadClipboard, len(payload))
	}
	return ExtendedClipboard{Flags: binary.BigEndian.Uint32(payload), Data: payload[clipFlagsLen:]}, nil
}

// CapsTextMax is the text size limit in a caps message: one u32 per format bit, in bit order,
// so text (bit 0) is first. ok is false when text is not offered.
func (e ExtendedClipboard) CapsTextMax() (uint32, bool) {
	if e.Flags&ClipText == 0 || len(e.Data) < clipSizeLen {
		return 0, false
	}
	return binary.BigEndian.Uint32(e.Data), true
}

// ExtendedCaps is a caps payload offering text up to textMax bytes and the given actions.
func ExtendedCaps(actions, textMax uint32) []byte {
	b := binary.BigEndian.AppendUint32(nil, ClipCaps|actions|ClipText)
	return binary.BigEndian.AppendUint32(b, textMax)
}

// ExtendedAction is a payload with only a flags word: request, peek or notify.
func ExtendedAction(action, formats uint32) []byte {
	return binary.BigEndian.AppendUint32(nil, action|formats)
}

// ExtendedProvideText is a provide payload for text: flags, then a zlib stream of the size and
// the UTF-8 text with CRLF line ends and a terminating NUL.
func ExtendedProvideText(text string) ([]byte, error) {
	data := []byte(toCRLF(text) + "\x00")
	var buf bytes.Buffer
	buf.Write(binary.BigEndian.AppendUint32(nil, ClipProvide|ClipText))
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(binary.BigEndian.AppendUint32(nil, uint32(len(data)))); err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ProvideText inflates a provide payload and returns its text (LF line ends, no NUL). It reads
// one size+data pair per format bit, in bit order, and never inflates more than limit bytes.
// ok is false when the payload carries no text.
func (e ExtendedClipboard) ProvideText(limit int) (text string, ok bool, err error) {
	zr, err := zlib.NewReader(bytes.NewReader(e.Data))
	if err != nil {
		return "", false, fmt.Errorf("%w: %w", ErrBadClipboard, err)
	}
	defer func() { _ = zr.Close() }()
	r := &io.LimitedReader{R: zr, N: int64(limit)}
	var raw []byte
	for formats := e.Formats(); formats != 0; formats &= formats - 1 {
		var size [clipSizeLen]byte
		if _, err := io.ReadFull(r, size[:]); err != nil {
			return "", false, provideReadErr(r, err, limit)
		}
		n := int64(binary.BigEndian.Uint32(size[:]))
		if n > r.N {
			return "", false, fmt.Errorf("%w: clipboard format of %d bytes", ErrTooLarge, n)
		}
		chunk := make([]byte, n)
		if _, err := io.ReadFull(r, chunk); err != nil {
			return "", false, provideReadErr(r, err, limit)
		}
		if uint32(1)<<bits.TrailingZeros32(formats) == ClipText {
			raw, ok = chunk, true
		}
	}
	if !ok {
		return "", false, nil
	}
	return strings.ReplaceAll(strings.TrimSuffix(string(raw), "\x00"), "\r\n", "\n"), true, nil
}

func provideReadErr(r *io.LimitedReader, err error, limit int) error {
	if r.N <= 0 {
		return fmt.Errorf("%w: clipboard over %d bytes", ErrTooLarge, limit)
	}
	return fmt.Errorf("%w: %w", ErrBadClipboard, err)
}

func toCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}
