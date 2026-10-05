// Package rfb is the server side of the RFB protocol (RFC 6143, the protocol VNC speaks):
// handshake for 3.3, 3.7 and 3.8 with pluggable security, client message decoding, the Tight-JPEG,
// Raw, RRE, DesktopSize and Cursor encoders, and the Extended Clipboard wire format. It does no
// I/O scheduling, so every byte is unit-testable. Package govnc builds a server on top of it.
package rfb
