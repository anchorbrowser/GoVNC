package rfb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ClientMessage is one decoded client-to-server message.
type ClientMessage interface{ clientMessage() }

// SetPixelFormat asks for a new pixel format for Raw rectangles.
type SetPixelFormat struct{ Format PixelFormat }

// SetEncodings lists the encodings the client accepts, in preference order.
type SetEncodings struct{ Encodings []int32 }

// FramebufferUpdateRequest asks for one FramebufferUpdate.
type FramebufferUpdateRequest struct {
	Incremental         bool
	X, Y, Width, Height uint16
}

// KeyEvent is a key press or release of an X11 keysym.
type KeyEvent struct {
	Down   bool
	Keysym uint32
}

// PointerEvent is the pointer position and the button mask.
type PointerEvent struct {
	Mask uint8
	X, Y uint16
}

// ClientCutText is the client clipboard; extended-clipboard payloads are kept raw.
type ClientCutText struct {
	Text []byte
	// Extended is set when the length was negative: Text is an extended-clipboard payload.
	Extended bool
}

func (SetPixelFormat) clientMessage()           {}
func (SetEncodings) clientMessage()             {}
func (FramebufferUpdateRequest) clientMessage() {}
func (KeyEvent) clientMessage()                 {}
func (PointerEvent) clientMessage()             {}
func (ClientCutText) clientMessage()            {}

// ErrUnknownMessage means the stream cannot be framed any further and must be closed.
var ErrUnknownMessage = errors.New("rfb: unknown client message type")

// ErrTooLarge means a length field exceeded the server's limit.
var ErrTooLarge = errors.New("rfb: client message too large")

// Body lengths after the type byte.
const (
	setPixelFormatLen = 3 + pixelFormatLen
	setEncodingsHdr   = 3
	fbUpdateReqLen    = 9
	keyEventLen       = 7
	pointerEventLen   = 5
	cutTextHdr        = 7
)

// ReadClientMessage reads exactly one client message from r.
func ReadClientMessage(r io.Reader) (ClientMessage, error) {
	var t [1]byte
	if _, err := io.ReadFull(r, t[:]); err != nil {
		return nil, err
	}
	switch t[0] {
	case MsgSetPixelFormat:
		b, err := readN(r, setPixelFormatLen)
		if err != nil {
			return nil, err
		}
		return SetPixelFormat{Format: parsePixelFormat(b[3:])}, nil
	case MsgSetEncodings:
		return readSetEncodings(r)
	case MsgFramebufferUpdateRequest:
		b, err := readN(r, fbUpdateReqLen)
		if err != nil {
			return nil, err
		}
		return FramebufferUpdateRequest{
			Incremental: b[0] != 0,
			X:           binary.BigEndian.Uint16(b[1:]), Y: binary.BigEndian.Uint16(b[3:]),
			Width: binary.BigEndian.Uint16(b[5:]), Height: binary.BigEndian.Uint16(b[7:]),
		}, nil
	case MsgKeyEvent:
		b, err := readN(r, keyEventLen)
		if err != nil {
			return nil, err
		}
		return KeyEvent{Down: b[0] != 0, Keysym: binary.BigEndian.Uint32(b[3:])}, nil
	case MsgPointerEvent:
		b, err := readN(r, pointerEventLen)
		if err != nil {
			return nil, err
		}
		return PointerEvent{Mask: b[0], X: binary.BigEndian.Uint16(b[1:]), Y: binary.BigEndian.Uint16(b[3:])}, nil
	case MsgClientCutText:
		return readCutText(r)
	default:
		return nil, fmt.Errorf("%w: %d", ErrUnknownMessage, t[0])
	}
}

func readN(r io.Reader, n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	return b, nil
}

func readSetEncodings(r io.Reader) (ClientMessage, error) {
	hdr, err := readN(r, setEncodingsHdr)
	if err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(hdr[1:]))
	if n > MaxEncodings {
		return nil, fmt.Errorf("%w: %d encodings", ErrTooLarge, n)
	}
	b, err := readN(r, 4*n)
	if err != nil {
		return nil, err
	}
	encs := make([]int32, n)
	for i := range encs {
		encs[i] = int32(binary.BigEndian.Uint32(b[4*i:]))
	}
	return SetEncodings{Encodings: encs}, nil
}

// readCutText reads ClientCutText. A negative length is the extended-clipboard form; its
// absolute value is the payload size either way.
func readCutText(r io.Reader) (ClientMessage, error) {
	hdr, err := readN(r, cutTextHdr)
	if err != nil {
		return nil, err
	}
	n := int64(int32(binary.BigEndian.Uint32(hdr[3:])))
	extended := n < 0
	if extended {
		n = -n
	}
	if n > MaxCutText {
		return nil, fmt.Errorf("%w: cut text of %d bytes", ErrTooLarge, n)
	}
	text, err := readN(r, int(n))
	if err != nil {
		return nil, err
	}
	return ClientCutText{Text: text, Extended: extended}, nil
}
