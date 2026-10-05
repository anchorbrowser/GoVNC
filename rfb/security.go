package rfb

import (
	"crypto/des"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"math/bits"
)

// SecurityVNCAuth is VNC Authentication (RFC 6143 7.2.2).
const SecurityVNCAuth uint8 = 2

// VNCAuthChallengeLen is the size of the VNC Authentication challenge and response.
const VNCAuthChallengeLen = 16

// ErrAuthFailed means the client failed the security type's exchange.
var ErrAuthFailed = errors.New("rfb: authentication failed")

// SecurityType is one security type the server offers. Authenticate runs after the client chose
// it (3.7, 3.8) or the server announced it (3.3); the handshake sends SecurityResult itself.
type SecurityType interface {
	// Type is the security-type number on the wire.
	Type() uint8
	// Authenticate runs the type's exchange on rw. A non-nil error fails the handshake; wrap
	// ErrAuthFailed for a wrong credential.
	Authenticate(rw io.ReadWriter) error
}

// NoSecurity is security type None: no authentication, no encryption.
type NoSecurity struct{}

// Type is SecurityNone.
func (NoSecurity) Type() uint8 { return SecurityNone }

// Authenticate has nothing to exchange.
func (NoSecurity) Authenticate(io.ReadWriter) error { return nil }

// VNCAuth is classic VNC Authentication: a 16-byte DES challenge keyed by the first 8 bytes of
// Password. It is weak (an 8-byte key, DES, no transport protection) and exists for client
// compatibility; protect the transport instead (TLS, an authenticated tunnel, localhost only).
type VNCAuth struct {
	Password string
	// Rand is the challenge source; nil is crypto/rand.
	Rand io.Reader
}

// Type is SecurityVNCAuth.
func (VNCAuth) Type() uint8 { return SecurityVNCAuth }

// Authenticate sends a random challenge and checks the client's DES response.
func (a VNCAuth) Authenticate(rw io.ReadWriter) error {
	if a.Password == "" {
		return errors.New("rfb: vnc auth has no password")
	}
	src := a.Rand
	if src == nil {
		src = rand.Reader
	}
	var challenge [VNCAuthChallengeLen]byte
	if _, err := io.ReadFull(src, challenge[:]); err != nil {
		return fmt.Errorf("rfb: vnc auth challenge: %w", err)
	}
	if _, err := rw.Write(challenge[:]); err != nil {
		return fmt.Errorf("rfb: writing vnc auth challenge: %w", err)
	}
	var got [VNCAuthChallengeLen]byte
	if _, err := io.ReadFull(rw, got[:]); err != nil {
		return fmt.Errorf("rfb: reading vnc auth response: %w", err)
	}
	want, err := VNCAuthResponse(a.Password, challenge)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
		return ErrAuthFailed
	}
	return nil
}

// VNCAuthResponse is what a client answers to challenge: challenge encrypted with DES in ECB mode
// under the password's first 8 bytes (zero padded), each key byte bit-reversed — the quirk every
// VNC implementation keeps from the original.
func VNCAuthResponse(password string, challenge [VNCAuthChallengeLen]byte) ([VNCAuthChallengeLen]byte, error) {
	var key [8]byte
	copy(key[:], password)
	for i, b := range key {
		key[i] = bits.Reverse8(b)
	}
	block, err := des.NewCipher(key[:])
	if err != nil {
		return [VNCAuthChallengeLen]byte{}, fmt.Errorf("rfb: vnc auth key: %w", err)
	}
	var out [VNCAuthChallengeLen]byte
	block.Encrypt(out[:8], challenge[:8])
	block.Encrypt(out[8:], challenge[8:])
	return out, nil
}
