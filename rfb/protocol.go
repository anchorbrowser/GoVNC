package rfb

// Protocol versions (RFC 6143 7.1.1).
const (
	VersionLen = 12
	Version33  = "RFB 003.003\n"
	Version37  = "RFB 003.007\n"
	Version38  = "RFB 003.008\n"
)

// Security types (RFC 6143 7.1.2, 7.1.3).
const (
	SecurityInvalid   uint8  = 0
	SecurityNone      uint8  = 1
	SecurityResultOK  uint32 = 0
	SecurityResultBad uint32 = 1
)

// Client-to-server message types (RFC 6143 7.5).
const (
	MsgSetPixelFormat           uint8 = 0
	MsgSetEncodings             uint8 = 2
	MsgFramebufferUpdateRequest uint8 = 3
	MsgKeyEvent                 uint8 = 4
	MsgPointerEvent             uint8 = 5
	MsgClientCutText            uint8 = 6
)

// Server-to-client message types (RFC 6143 7.6).
const (
	MsgFramebufferUpdate uint8 = 0
)

// Encodings (RFC 6143 7.7, 7.8; Tight from the community rfbproto).
const (
	EncodingRaw         int32 = 0
	EncodingRRE         int32 = 2
	EncodingTight       int32 = 7
	EncodingDesktopSize int32 = -223
)

// Tight compression-control values.
const (
	// TightJPEG is bits 7-4 = 1001: a JPEG sub-rectangle.
	TightJPEG byte = 0x90
	// TightMaxLength is the largest compact length a 3-byte field carries (noVNC reads 7+7+8 bits).
	TightMaxLength = 1<<22 - 1
)

// Pointer button mask bits (RFC 6143 7.5.5; 3-6 are the wheel by convention).
const (
	ButtonLeft       uint8 = 1 << 0
	ButtonMiddle     uint8 = 1 << 1
	ButtonRight      uint8 = 1 << 2
	ButtonWheelUp    uint8 = 1 << 3
	ButtonWheelDown  uint8 = 1 << 4
	ButtonWheelLeft  uint8 = 1 << 5
	ButtonWheelRight uint8 = 1 << 6
)

// Limits the server enforces on client input.
const (
	// MaxEncodings bounds SetEncodings; real clients send about 30.
	MaxEncodings = 1024
	// MaxCutText bounds ClientCutText; anything longer closes the connection.
	MaxCutText = 1 << 20
	// MaxNameLen bounds the desktop name in ServerInit.
	MaxNameLen = 256
)
