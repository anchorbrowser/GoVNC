package govnc

import (
	"log/slog"
	"testing"

	"github.com/anchorbrowser/govnc/rfb"
)

func TestClipboardOutboxCapDropsOldest(t *testing.T) {
	c := &sessionClipboard{log: slog.New(slog.DiscardHandler)}

	// Fill the outbox beyond capacity.
	for i := 0; i < clipOutboxMax+3; i++ {
		c.queue([]byte{byte(i)}, false)
	}

	taken := c.take()
	eq(t, clipOutboxMax, len(taken))

	// The oldest messages (0, 1, 2) should be dropped.
	// Messages 3 through 10 should remain.
	for i, msg := range taken {
		expected := byte(i + 3)
		if msg[8] != expected {
			t.Fatalf("position %d: want payload byte %d, got %d", i, expected, msg[8])
		}
	}
}

func TestClipboardTakeDrains(t *testing.T) {
	c := &sessionClipboard{log: slog.New(slog.DiscardHandler)}

	c.queue([]byte{1}, false)
	c.queue([]byte{2}, false)

	first := c.take()
	eq(t, 2, len(first))

	second := c.take()
	if second != nil {
		t.Fatal("take after drain should return nil")
	}

	// Queue more after draining.
	c.queue([]byte{3}, false)
	third := c.take()
	eq(t, 1, len(third))
}

func TestClipboardSetEncodingsExtended(t *testing.T) {
	c := &sessionClipboard{log: slog.New(slog.DiscardHandler)}

	// SetEncodings with extended clipboard queues caps.
	queued := c.setEncodings([]int32{rfb.EncodingTight, rfb.EncodingExtendedClipboard})
	eq(t, true, queued)
	eq(t, true, c.extended)

	msgs := c.take()
	eq(t, 1, len(msgs))

	// Verify it's an extended message.
	if len(msgs[0]) < 8 {
		t.Fatal("caps message too short")
	}
	// Extended ServerCutText has negative length.
	length := int32(msgs[0][4])<<24 | int32(msgs[0][5])<<16 | int32(msgs[0][6])<<8 | int32(msgs[0][7])
	if length >= 0 {
		t.Fatal("caps message not extended")
	}
}

func TestClipboardSetEncodingsClassic(t *testing.T) {
	c := &sessionClipboard{log: slog.New(slog.DiscardHandler)}

	// First enable extended.
	c.setEncodings([]int32{rfb.EncodingExtendedClipboard})
	c.take()

	// Then disable it.
	queued := c.setEncodings([]int32{rfb.EncodingTight})
	eq(t, false, queued)
	eq(t, false, c.extended)
}

func TestClipboardFromViewerClassic(t *testing.T) {
	c := &sessionClipboard{log: slog.New(slog.DiscardHandler)}

	text, ok, replied := c.fromViewer(rfb.ClientCutText{
		Text:     []byte("test\r\ntext"),
		Extended: false,
	})

	eq(t, true, ok)
	eq(t, false, replied)
	eq(t, "test\ntext", text) // CRLF converted to LF
}

func TestClipboardFromViewerClassicLatin1(t *testing.T) {
	c := &sessionClipboard{log: slog.New(slog.DiscardHandler)}

	// 0xe9 is é in Latin-1
	text, ok, replied := c.fromViewer(rfb.ClientCutText{
		Text:     []byte("caf\xe9"),
		Extended: false,
	})

	eq(t, true, ok)
	eq(t, false, replied)
	eq(t, "café", text)
}

func TestClipboardOfferClassic(t *testing.T) {
	c := &sessionClipboard{log: slog.New(slog.DiscardHandler)}

	queued := c.offer("naïve ☃")
	eq(t, true, queued)

	msgs := c.take()
	eq(t, 1, len(msgs))

	// Classic ServerCutText has positive length.
	length := int32(msgs[0][4])<<24 | int32(msgs[0][5])<<16 | int32(msgs[0][6])<<8 | int32(msgs[0][7])
	if length < 0 {
		t.Fatal("classic message should have positive length")
	}
}

func TestClipboardOfferExtendedProvide(t *testing.T) {
	c := &sessionClipboard{log: slog.New(slog.DiscardHandler)}

	// Enable extended with default caps (unsolicited up to 1 MiB).
	c.setEncodings([]int32{rfb.EncodingExtendedClipboard})
	c.take()

	queued := c.offer("test")
	eq(t, true, queued)

	msgs := c.take()
	eq(t, 1, len(msgs))

	// Extended message has negative length.
	length := int32(msgs[0][4])<<24 | int32(msgs[0][5])<<16 | int32(msgs[0][6])<<8 | int32(msgs[0][7])
	if length >= 0 {
		t.Fatal("extended message should have negative length")
	}
}

func TestClipboardOfferExtendedNotify(t *testing.T) {
	c := &sessionClipboard{log: slog.New(slog.DiscardHandler)}

	// Enable extended.
	c.setEncodings([]int32{rfb.EncodingExtendedClipboard})
	c.take()

	// Set client caps to zero unsolicited text.
	c.mu.Lock()
	c.clientTextMax = 0
	c.mu.Unlock()

	queued := c.offer("large text")
	eq(t, true, queued)

	msgs := c.take()
	eq(t, 1, len(msgs))
}

func TestClipboardSetText(t *testing.T) {
	c := &sessionClipboard{log: slog.New(slog.DiscardHandler)}

	c.setText("first")
	eq(t, "first", c.text)

	c.setText("second")
	eq(t, "second", c.text)
}

func TestClipboardFromViewerExtendedNotifyRequest(t *testing.T) {
	c := &sessionClipboard{log: slog.New(slog.DiscardHandler)}
	c.setEncodings([]int32{rfb.EncodingExtendedClipboard})
	c.take()

	// Viewer notifies it has text.
	notify := rfb.ExtendedAction(rfb.ClipNotify, rfb.ClipText)
	text, ok, replied := c.fromViewer(rfb.ClientCutText{
		Text:     notify,
		Extended: true,
	})

	eq(t, false, ok)
	eq(t, true, replied) // Server queues a request.
	eq(t, "", text)

	msgs := c.take()
	eq(t, 1, len(msgs))
}

func TestClipboardFromViewerExtendedPeek(t *testing.T) {
	c := &sessionClipboard{log: slog.New(slog.DiscardHandler)}
	c.setEncodings([]int32{rfb.EncodingExtendedClipboard})
	c.take()

	// Peek with no server clipboard.
	peek := rfb.ExtendedAction(rfb.ClipPeek, 0)
	text, ok, replied := c.fromViewer(rfb.ClientCutText{
		Text:     peek,
		Extended: true,
	})

	eq(t, false, ok)
	eq(t, true, replied)
	eq(t, "", text)

	msgs := c.take()
	eq(t, 1, len(msgs))

	// Now set server clipboard and peek again.
	c.setText("something")
	_, ok, replied = c.fromViewer(rfb.ClientCutText{
		Text:     peek,
		Extended: true,
	})

	eq(t, false, ok)
	eq(t, true, replied)

	msgs = c.take()
	eq(t, 1, len(msgs))
}

func TestClipboardFromViewerExtendedRequest(t *testing.T) {
	c := &sessionClipboard{log: slog.New(slog.DiscardHandler)}
	c.setEncodings([]int32{rfb.EncodingExtendedClipboard})
	c.take()

	c.setText("server text")

	request := rfb.ExtendedAction(rfb.ClipRequest, rfb.ClipText)
	text, ok, replied := c.fromViewer(rfb.ClientCutText{
		Text:     request,
		Extended: true,
	})

	eq(t, false, ok)
	eq(t, true, replied)
	eq(t, "", text)

	msgs := c.take()
	eq(t, 1, len(msgs))
}

func TestClipboardFromViewerMalformed(t *testing.T) {
	c := &sessionClipboard{log: slog.New(slog.DiscardHandler)}

	// Malformed extended clipboard is ignored.
	text, ok, replied := c.fromViewer(rfb.ClientCutText{
		Text:     []byte{1}, // Too short to parse.
		Extended: true,
	})

	eq(t, false, ok)
	eq(t, false, replied)
	eq(t, "", text)
}
