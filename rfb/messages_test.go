package rfb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
)

func TestReadClientMessage(t *testing.T) {
	pf := DefaultPixelFormat()
	pf.RedShift, pf.BlueShift = 0, 16
	setPF := append([]byte{MsgSetPixelFormat, 0, 0, 0}, pf.marshal()...)

	encs := []byte{MsgSetEncodings, 0, 0, 3}
	for _, e := range []int32{EncodingTight, EncodingRaw, EncodingDesktopSize} {
		encs = binary.BigEndian.AppendUint32(encs, uint32(e))
	}
	cut := []byte{MsgClientCutText, 0, 0, 0, 0, 0, 0, 2, 'h', 'i'}
	extCut := []byte{MsgClientCutText, 0, 0, 0, 0xff, 0xff, 0xff, 0xfe, 1, 2}

	tests := []struct {
		name string
		in   []byte
		want ClientMessage
	}{
		{"SetPixelFormat", setPF, SetPixelFormat{Format: pf}},
		{"SetEncodings", encs, SetEncodings{Encodings: []int32{EncodingTight, EncodingRaw, EncodingDesktopSize}}},
		{"full FramebufferUpdateRequest", []byte{MsgFramebufferUpdateRequest, 0, 0, 1, 0, 2, 0x05, 0xa0, 0x03, 0x84}, FramebufferUpdateRequest{X: 1, Y: 2, Width: 1440, Height: 900}},
		{"incremental FramebufferUpdateRequest", []byte{MsgFramebufferUpdateRequest, 1, 0, 0, 0, 0, 0, 10, 0, 10}, FramebufferUpdateRequest{Incremental: true, Width: 10, Height: 10}},
		{"KeyEvent down", []byte{MsgKeyEvent, 1, 0, 0, 0, 0, 0xff, 0x0d}, KeyEvent{Down: true, Keysym: 0xff0d}},
		{"PointerEvent", []byte{MsgPointerEvent, ButtonLeft, 0, 10, 0, 20}, PointerEvent{Mask: ButtonLeft, X: 10, Y: 20}},
		{"ClientCutText", cut, ClientCutText{Text: []byte("hi")}},
		{"extended ClientCutText", extCut, ClientCutText{Text: []byte{1, 2}, Extended: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReadClientMessage(bytes.NewReader(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestReadClientMessageErrors(t *testing.T) {
	huge := binary.BigEndian.AppendUint32([]byte{MsgClientCutText, 0, 0, 0}, MaxCutText+1)
	tests := []struct {
		name   string
		in     []byte
		target error
	}{
		{"unknown type", []byte{250, 0}, ErrUnknownMessage},
		{"cut text over limit", huge, ErrTooLarge},
		{"too many encodings", []byte{MsgSetEncodings, 0, 0xff, 0xff}, ErrTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ReadClientMessage(bytes.NewReader(tt.in)); !errors.Is(err, tt.target) {
				t.Fatalf("err %v, want %v", err, tt.target)
			}
		})
	}
	if _, err := ReadClientMessage(bytes.NewReader([]byte{MsgKeyEvent, 1})); err == nil {
		t.Fatal("truncated KeyEvent must fail")
	}
}
