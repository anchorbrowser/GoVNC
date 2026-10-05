package govnc

import (
	"context"

	"github.com/anchorbrowser/govnc/rfb"
)

// KeyHandler handles a key event and reports whether it consumed it.
type KeyHandler func(ctx context.Context, ev rfb.KeyEvent) bool

// PointerHandler handles a pointer event and reports whether it consumed it.
type PointerHandler func(ctx context.Context, ev rfb.PointerEvent) bool

// ClipboardHandler handles the viewer's clipboard text (classic Latin-1 decoded, CRLF as LF, or
// Extended Clipboard UTF-8) and reports whether it consumed it.
type ClipboardHandler func(ctx context.Context, text string) bool

// Hooks are one session's input handlers. Each list runs in order on the session's reader
// goroutine until a handler consumes the event, so a chain such as "OS input, then an open menu,
// then the clipboard, then the default" is a list. ctx ends with the session. A slow handler
// delays that viewer's next message, never another viewer or the frames.
type Hooks struct {
	Key       []KeyHandler
	Pointer   []PointerHandler
	Clipboard []ClipboardHandler
	// Closed is called once the session has ended, with the error that ended it (io.EOF when the
	// viewer hung up).
	Closed func(err error)
}

// HandleKey runs the key chain and reports whether a handler consumed ev.
func (h Hooks) HandleKey(ctx context.Context, ev rfb.KeyEvent) bool {
	for _, fn := range h.Key {
		if fn(ctx, ev) {
			return true
		}
	}
	return false
}

// HandlePointer runs the pointer chain and reports whether a handler consumed ev.
func (h Hooks) HandlePointer(ctx context.Context, ev rfb.PointerEvent) bool {
	for _, fn := range h.Pointer {
		if fn(ctx, ev) {
			return true
		}
	}
	return false
}

// HandleClipboard runs the clipboard chain and reports whether a handler consumed text.
func (h Hooks) HandleClipboard(ctx context.Context, text string) bool {
	for _, fn := range h.Clipboard {
		if fn(ctx, text) {
			return true
		}
	}
	return false
}
