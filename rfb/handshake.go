package rfb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// PixelFormat is the 16-byte PIXEL_FORMAT (RFC 6143 7.4).
type PixelFormat struct {
	BitsPerPixel uint8
	Depth        uint8
	BigEndian    bool
	TrueColour   bool
	RedMax       uint16
	GreenMax     uint16
	BlueMax      uint16
	RedShift     uint8
	GreenShift   uint8
	BlueShift    uint8
}

const pixelFormatLen = 16

// DefaultPixelFormat is what ServerInit announces: 32 bpp, depth 24, little-endian, true colour,
// red at bit 16, green at 8, blue at 0.
func DefaultPixelFormat() PixelFormat {
	return PixelFormat{
		BitsPerPixel: 32, Depth: 24, TrueColour: true,
		RedMax: 255, GreenMax: 255, BlueMax: 255,
		RedShift: 16, GreenShift: 8, BlueShift: 0,
	}
}

// Validate accepts any true-colour format of 8, 16 or 32 bpp whose channels fit in the pixel.
func (pf PixelFormat) Validate() error {
	if !pf.TrueColour {
		return errors.New("colour-map pixel formats are not supported")
	}
	switch pf.BitsPerPixel {
	case 8, 16, 32:
	default:
		return fmt.Errorf("%d bits per pixel is not supported", pf.BitsPerPixel)
	}
	for _, ch := range []struct {
		max   uint16
		shift uint8
	}{{pf.RedMax, pf.RedShift}, {pf.GreenMax, pf.GreenShift}, {pf.BlueMax, pf.BlueShift}} {
		if ch.max == 0 || int(ch.shift)+bitLen(ch.max) > int(pf.BitsPerPixel) {
			return fmt.Errorf("channel max %d at shift %d does not fit %d bpp", ch.max, ch.shift, pf.BitsPerPixel)
		}
	}
	return nil
}

func bitLen(v uint16) int {
	n := 0
	for ; v != 0; v >>= 1 {
		n++
	}
	return n
}

func (pf PixelFormat) marshal() []byte {
	b := make([]byte, pixelFormatLen)
	b[0], b[1] = pf.BitsPerPixel, pf.Depth
	b[2], b[3] = boolByte(pf.BigEndian), boolByte(pf.TrueColour)
	binary.BigEndian.PutUint16(b[4:], pf.RedMax)
	binary.BigEndian.PutUint16(b[6:], pf.GreenMax)
	binary.BigEndian.PutUint16(b[8:], pf.BlueMax)
	b[10], b[11], b[12] = pf.RedShift, pf.GreenShift, pf.BlueShift
	return b
}

func parsePixelFormat(b []byte) PixelFormat {
	return PixelFormat{
		BitsPerPixel: b[0], Depth: b[1], BigEndian: b[2] != 0, TrueColour: b[3] != 0,
		RedMax: binary.BigEndian.Uint16(b[4:]), GreenMax: binary.BigEndian.Uint16(b[6:]), BlueMax: binary.BigEndian.Uint16(b[8:]),
		RedShift: b[10], GreenShift: b[11], BlueShift: b[12],
	}
}

func boolByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}

// ServerInit is what the server tells a client after security.
type ServerInit struct {
	Width, Height uint16
	Format        PixelFormat
	Name          string
}

// Marshal encodes the ServerInit message (RFC 6143 7.3.2).
func (s ServerInit) Marshal() []byte {
	name := s.Name
	if len(name) > MaxNameLen {
		name = name[:MaxNameLen]
	}
	b := make([]byte, 0, 4+pixelFormatLen+4+len(name))
	b = binary.BigEndian.AppendUint16(b, s.Width)
	b = binary.BigEndian.AppendUint16(b, s.Height)
	b = append(b, s.Format.marshal()...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(name)))
	return append(b, name...)
}

// Handshake is the result of a completed server handshake.
type Handshake struct {
	// Version is the protocol the session speaks: Version33, Version37 or Version38.
	Version string
	// Security is the security type the client passed.
	Security uint8
	Shared   bool
}

// ErrSecurityRejected is returned when the client picks a security type the server did not offer.
var ErrSecurityRejected = errors.New("rfb: client chose a security type that was not offered")

const (
	rejectedReason   = "security type not offered"
	authFailedReason = "authentication failed"
)

// ServerHandshake runs ProtocolVersion, Security, SecurityResult, ClientInit and ServerInit on rw,
// offering security in order (none given: NoSecurity). A 3.3 client gets the first type, since in
// 3.3 the server decides. The caller sets deadlines.
func ServerHandshake(rw io.ReadWriter, init ServerInit, security ...SecurityType) (Handshake, error) {
	if len(security) == 0 {
		security = []SecurityType{NoSecurity{}}
	}
	if len(security) > 255 {
		return Handshake{}, errors.New("rfb: more than 255 security types")
	}
	if _, err := io.WriteString(rw, Version38); err != nil {
		return Handshake{}, fmt.Errorf("rfb: writing version: %w", err)
	}
	raw := make([]byte, VersionLen)
	if _, err := io.ReadFull(rw, raw); err != nil {
		return Handshake{}, fmt.Errorf("rfb: reading client version: %w", err)
	}
	version, err := negotiateVersion(string(raw))
	if err != nil {
		return Handshake{}, err
	}
	sec, err := serverSecurity(rw, version, security)
	if err != nil {
		return Handshake{}, err
	}
	var shared [1]byte
	if _, err := io.ReadFull(rw, shared[:]); err != nil {
		return Handshake{}, fmt.Errorf("rfb: reading ClientInit: %w", err)
	}
	if _, err := rw.Write(init.Marshal()); err != nil {
		return Handshake{}, fmt.Errorf("rfb: writing ServerInit: %w", err)
	}
	return Handshake{Version: version, Security: sec, Shared: shared[0] != 0}, nil
}

// negotiateVersion maps the client's ProtocolVersion to the one spoken. RFC 6143 7.1.1: a
// version other than 3.3, 3.7 and 3.8 is treated as 3.3; anything newer than 3.8 speaks 3.8.
func negotiateVersion(v string) (string, error) {
	if len(v) != VersionLen || v[:4] != "RFB " || v[7] != '.' || v[11] != '\n' {
		return "", fmt.Errorf("rfb: bad client version %q", v)
	}
	major, errMajor := strconv.Atoi(v[4:7])
	minor, errMinor := strconv.Atoi(v[8:11])
	if errMajor != nil || errMinor != nil || major < 3 {
		return "", fmt.Errorf("rfb: bad client version %q", v)
	}
	switch {
	case major > 3 || minor >= 8:
		return Version38, nil
	case minor == 7:
		return Version37, nil
	default:
		return Version33, nil
	}
}

func serverSecurity(rw io.ReadWriter, version string, offered []SecurityType) (uint8, error) {
	var chosen SecurityType
	if version == Version33 {
		chosen = offered[0]
		if _, err := rw.Write(binary.BigEndian.AppendUint32(nil, uint32(chosen.Type()))); err != nil {
			return 0, fmt.Errorf("rfb: writing security type: %w", err)
		}
	} else {
		list := make([]byte, 0, 1+len(offered))
		list = append(list, byte(len(offered)))
		for _, s := range offered {
			list = append(list, s.Type())
		}
		if _, err := rw.Write(list); err != nil {
			return 0, fmt.Errorf("rfb: writing security types: %w", err)
		}
		var pick [1]byte
		if _, err := io.ReadFull(rw, pick[:]); err != nil {
			return 0, fmt.Errorf("rfb: reading security choice: %w", err)
		}
		for _, s := range offered {
			if s.Type() == pick[0] {
				chosen = s
				break
			}
		}
		if chosen == nil {
			if version == Version38 {
				writeFailure(rw, version, rejectedReason)
			}
			return 0, fmt.Errorf("%w: %d", ErrSecurityRejected, pick[0])
		}
	}
	if err := chosen.Authenticate(rw); err != nil {
		if chosen.Type() != SecurityNone {
			writeFailure(rw, version, authFailedReason)
		}
		return 0, err
	}
	// 3.3 and 3.7 send no SecurityResult for None (RFC 6143 appendix A).
	if version == Version38 || chosen.Type() != SecurityNone {
		if _, err := rw.Write(binary.BigEndian.AppendUint32(nil, SecurityResultOK)); err != nil {
			return 0, fmt.Errorf("rfb: writing security result: %w", err)
		}
	}
	return chosen.Type(), nil
}

// writeFailure sends a failed SecurityResult, with the reason in 3.8. Best effort: the caller
// closes the connection on the error it returns either way.
func writeFailure(rw io.Writer, version, reason string) {
	msg := binary.BigEndian.AppendUint32(nil, SecurityResultBad)
	if version == Version38 {
		msg = binary.BigEndian.AppendUint32(msg, uint32(len(reason)))
		msg = append(msg, reason...)
	}
	_, _ = rw.Write(msg)
}
