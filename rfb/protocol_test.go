package rfb

import "testing"

func TestVersionStrings(t *testing.T) {
	for _, v := range []string{Version33, Version37, Version38} {
		if len(v) != VersionLen || v[:4] != "RFB " || v[len(v)-1] != '\n' {
			t.Errorf("version %q must be %d bytes, \"RFB xxx.yyy\\n\"", v, VersionLen)
		}
	}
}

// Button bits are single, distinct bits; noVNC sends the wheel as bits 3-6.
func TestButtonMaskBits(t *testing.T) {
	var seen uint8
	for i, b := range []uint8{ButtonLeft, ButtonMiddle, ButtonRight, ButtonWheelUp, ButtonWheelDown, ButtonWheelLeft, ButtonWheelRight} {
		if b != 1<<i {
			t.Fatalf("button %d = %#x, want %#x", i, b, uint8(1<<i))
		}
		if seen&b != 0 {
			t.Fatalf("button %#x repeated", b)
		}
		seen |= b
	}
}

func TestMessageAndEncodingValues(t *testing.T) {
	// RFC 6143 numbers; the wire depends on them.
	got := []uint8{MsgSetPixelFormat, MsgSetEncodings, MsgFramebufferUpdateRequest, MsgKeyEvent, MsgPointerEvent, MsgClientCutText}
	want := []uint8{0, 2, 3, 4, 5, 6}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("client message %d = %d, want %d", i, got[i], want[i])
		}
	}
	if EncodingRaw != 0 || EncodingRRE != 2 || EncodingTight != 7 || EncodingDesktopSize != -223 || MsgFramebufferUpdate != 0 {
		t.Fatal("encoding numbers changed")
	}
	if SecurityNone != 1 || SecurityInvalid != 0 || SecurityResultOK != 0 || SecurityResultBad != 1 {
		t.Fatal("security numbers changed")
	}
	if TightJPEG>>4 != 0x9 || TightMaxLength != 4194303 {
		t.Fatalf("tight constants %#x %d", TightJPEG, TightMaxLength)
	}
	if MaxCutText <= 0 || MaxEncodings <= 0 || MaxNameLen <= 0 || MaxNameLen > MaxCutText {
		t.Fatal("limits must be positive and ordered")
	}
}
