package govnc

import (
	"bytes"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/anchorbrowser/govnc/rfb"
)

const (
	// clipMaxBytes caps text in both directions; classic ClientCutText is capped by the parser.
	clipMaxBytes = rfb.MaxCutText
	// clipActions is what the server offers in its extended caps.
	clipActions = rfb.ClipRequest | rfb.ClipPeek | rfb.ClipNotify | rfb.ClipProvide
	// clipClientDefault is what a client that sent no caps supports (rfbproto).
	clipClientDefault = rfb.ClipRequest | rfb.ClipNotify | rfb.ClipProvide
	clipOutboxMax     = 8
)

// sessionClipboard is one viewer's clipboard wire state: classic ServerCutText/ClientCutText or
// the Extended Clipboard, and the messages waiting for the writer.
type sessionClipboard struct {
	log *slog.Logger

	mu            sync.Mutex
	extended      bool
	clientActions uint32
	clientText    bool
	clientTextMax uint32
	// text is the server clipboard this viewer is offered; it answers request and peek.
	text   string
	outbox [][]byte
}

// setEncodings switches the extended format on or off. Every SetEncodings that offers it gets a
// caps message (rfbproto); the client's caps are back to the defaults until it answers. It
// reports whether a message was queued.
func (c *sessionClipboard) setEncodings(encs []int32) bool {
	ext := slices.Contains(encs, rfb.EncodingExtendedClipboard)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.extended = ext
	if ext {
		c.clientActions, c.clientText, c.clientTextMax = clipClientDefault, true, rfb.ClipDefaultTextMax
		c.queue(rfb.ExtendedCaps(clipActions, clipMaxBytes), true)
	}
	return ext
}

// fromViewer handles ClientCutText. text is the viewer's clipboard when ok; replied reports a
// queued answer.
func (c *sessionClipboard) fromViewer(m rfb.ClientCutText) (text string, ok, replied bool) {
	if !m.Extended {
		return strings.ReplaceAll(rfb.DecodeLatin1(m.Text), "\r\n", "\n"), true, false
	}
	e, err := rfb.ParseExtendedClipboard(m.Text)
	if err != nil {
		c.log.Debug("govnc: bad extended clipboard", "error", err)
		return "", false, false
	}
	hasText := e.Formats()&rfb.ClipText != 0
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case e.Actions()&rfb.ClipCaps != 0:
		limit, ok := e.CapsTextMax()
		c.clientActions, c.clientText, c.clientTextMax = e.Actions(), ok, limit
	case e.Actions() == rfb.ClipNotify && hasText:
		c.queue(rfb.ExtendedAction(rfb.ClipRequest, rfb.ClipText), true)
		return "", false, true
	case e.Actions() == rfb.ClipProvide:
		s, ok, err := e.ProvideText(clipMaxBytes)
		if err != nil {
			c.log.Debug("govnc: extended clipboard ignored", "error", err)
			return "", false, false
		}
		return s, ok, false
	case e.Actions() == rfb.ClipRequest && hasText:
		if c.text != "" {
			return "", false, c.queueProvide(c.text)
		}
	case e.Actions() == rfb.ClipPeek:
		formats := uint32(0)
		if c.text != "" {
			formats = rfb.ClipText
		}
		c.queue(rfb.ExtendedAction(rfb.ClipNotify, formats), true)
		return "", false, true
	}
	return "", false, false
}

// offer makes text this viewer's server clipboard and queues it: classic Latin-1, or extended
// provide when it fits the client's unsolicited limit, else notify so the client requests it.
func (c *sessionClipboard) offer(text string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.text = text
	switch {
	case !c.extended:
		c.queue(rfb.EncodeLatin1(text), false)
		return true
	case !c.clientText:
		return false
	case c.clientActions&rfb.ClipProvide != 0 && uint64(len(text)) < uint64(c.clientTextMax):
		return c.queueProvide(text)
	case c.clientActions&rfb.ClipNotify != 0:
		c.queue(rfb.ExtendedAction(rfb.ClipNotify, rfb.ClipText), true)
		return true
	}
	return false
}

func (c *sessionClipboard) setText(text string) {
	c.mu.Lock()
	c.text = text
	c.mu.Unlock()
}

// queueProvide queues text as an extended provide. c.mu is held.
func (c *sessionClipboard) queueProvide(text string) bool {
	payload, err := rfb.ExtendedProvideText(text)
	if err != nil {
		c.log.Debug("govnc: clipboard not compressed", "error", err)
		return false
	}
	c.queue(payload, true)
	return true
}

// queue builds ServerCutText and keeps it for the writer; a full outbox drops the oldest. c.mu
// is held.
func (c *sessionClipboard) queue(data []byte, extended bool) {
	var buf bytes.Buffer
	write := rfb.WriteServerCutText
	if extended {
		write = rfb.WriteExtendedServerCutText
	}
	if err := write(&buf, data); err != nil {
		c.log.Debug("govnc: clipboard not sent", "error", err)
		return
	}
	if len(c.outbox) >= clipOutboxMax {
		c.log.Debug("govnc: clipboard outbox full, oldest dropped")
		c.outbox = c.outbox[1:]
	}
	c.outbox = append(c.outbox, buf.Bytes())
}

// take empties the outbox for the writer.
func (c *sessionClipboard) take() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.outbox
	c.outbox = nil
	return out
}
