package rfb

import (
	"bytes"
	"crypto/des"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// fixedRand is a challenge source that repeats one byte.
type fixedRand byte

func (f fixedRand) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(f)
	}
	return len(p), nil
}

func TestVNCAuthResponseReversesKeyBits(t *testing.T) {
	var challenge [VNCAuthChallengeLen]byte
	for i := range challenge {
		challenge[i] = byte(i * 17)
	}
	got, err := VNCAuthResponse("pass", challenge)
	if err != nil {
		t.Fatal(err)
	}
	// "pass" is 70 61 73 73; bit-reversed 0e 86 ce ce, zero padded to 8 bytes.
	block, err := des.NewCipher([]byte{0x0e, 0x86, 0xce, 0xce, 0, 0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	var want [VNCAuthChallengeLen]byte
	block.Encrypt(want[:8], challenge[:8])
	block.Encrypt(want[8:], challenge[8:])
	if got != want {
		t.Fatalf("response % x, want % x", got, want)
	}
	// Only the first 8 bytes of the password count.
	long, _ := VNCAuthResponse("12345678-ignored", challenge)
	short, _ := VNCAuthResponse("12345678", challenge)
	if long != short {
		t.Fatal("bytes after the eighth must not change the key")
	}
}

// handshakeCase is one client run through the security exchange.
type handshakeCase struct {
	name     string
	version  string
	offered  []SecurityType
	password string
	// wantList is the security list a 3.7/3.8 client reads (or the 3.3 type word).
	wantList []byte
	// pick is what a 3.7/3.8 client chooses.
	pick    uint8
	wantOK  bool
	wantErr error
}

func TestServerHandshakeSecurity(t *testing.T) {
	vnc := VNCAuth{Password: "secret", Rand: fixedRand(7)}
	tests := []handshakeCase{
		{name: "3.8 VNC auth ok", version: Version38, offered: []SecurityType{vnc}, password: "secret", wantList: []byte{1, 2}, pick: 2, wantOK: true},
		{name: "3.8 VNC auth wrong password", version: Version38, offered: []SecurityType{vnc}, password: "nope", wantList: []byte{1, 2}, pick: 2, wantErr: ErrAuthFailed},
		{name: "3.7 VNC auth ok", version: Version37, offered: []SecurityType{vnc}, password: "secret", wantList: []byte{1, 2}, pick: 2, wantOK: true},
		{name: "3.3 VNC auth ok", version: Version33, offered: []SecurityType{vnc}, password: "secret", wantList: []byte{0, 0, 0, 2}, wantOK: true},
		{name: "3.3 VNC auth wrong password", version: Version33, offered: []SecurityType{vnc}, password: "nope", wantList: []byte{0, 0, 0, 2}, wantErr: ErrAuthFailed},
		{name: "3.8 both offered, client takes None", version: Version38, offered: []SecurityType{vnc, NoSecurity{}}, wantList: []byte{2, 2, 1}, pick: 1, wantOK: true},
		{name: "3.8 client picks a type not offered", version: Version38, offered: []SecurityType{vnc}, wantList: []byte{1, 2}, pick: 1, wantErr: ErrSecurityRejected},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, client := pipe(t)
			init := ServerInit{Width: 4, Height: 2, Format: DefaultPixelFormat(), Name: "x"}
			type result struct {
				hs  Handshake
				err error
			}
			done := make(chan result, 1)
			go func() {
				hs, err := ServerHandshake(server, init, tt.offered...)
				if err != nil {
					_ = server.Close()
				}
				done <- result{hs, err}
			}()
			mustRead(t, client, VersionLen)
			mustWrite(t, client, []byte(tt.version))
			if got := mustRead(t, client, len(tt.wantList)); !bytes.Equal(got, tt.wantList) {
				t.Fatalf("security list %v, want %v", got, tt.wantList)
			}
			chosen := tt.pick
			if tt.version == Version33 {
				chosen = tt.wantList[3]
			} else {
				mustWrite(t, client, []byte{tt.pick})
			}
			offeredPick := false
			for _, s := range tt.offered {
				offeredPick = offeredPick || s.Type() == chosen
			}
			if chosen == SecurityVNCAuth && offeredPick {
				var challenge [VNCAuthChallengeLen]byte
				copy(challenge[:], mustRead(t, client, VNCAuthChallengeLen))
				resp, err := VNCAuthResponse(tt.password, challenge)
				if err != nil {
					t.Fatal(err)
				}
				mustWrite(t, client, resp[:])
			}
			hasResult := tt.version == Version38 || chosen != SecurityNone
			if !offeredPick && tt.version != Version38 {
				hasResult = false
			}
			if hasResult {
				word := binary.BigEndian.Uint32(mustRead(t, client, 4))
				if (word == SecurityResultOK) != tt.wantOK {
					t.Fatalf("SecurityResult %d, want ok=%v", word, tt.wantOK)
				}
				if !tt.wantOK && tt.version == Version38 {
					reason := mustRead(t, client, int(binary.BigEndian.Uint32(mustRead(t, client, 4))))
					if len(reason) == 0 {
						t.Fatal("3.8 failure must carry a reason")
					}
				}
			}
			if tt.wantOK {
				mustWrite(t, client, []byte{0})
				mustRead(t, client, len(init.Marshal()))
			}
			res := <-done
			if tt.wantErr != nil {
				if !errors.Is(res.err, tt.wantErr) {
					t.Fatalf("err %v, want %v", res.err, tt.wantErr)
				}
				return
			}
			if res.err != nil || res.hs.Security != chosen || res.hs.Shared {
				t.Fatalf("handshake %+v err %v", res.hs, res.err)
			}
		})
	}
}

func TestVNCAuthRefusesAnEmptyPassword(t *testing.T) {
	var buf bytes.Buffer
	if err := (VNCAuth{}).Authenticate(&buf); err == nil {
		t.Fatal("an empty password must not authenticate anyone")
	}
	if buf.Len() != 0 {
		t.Fatal("no challenge may be sent without a password")
	}
}

func TestVNCAuthChallengeSourceError(t *testing.T) {
	var buf bytes.Buffer
	if err := (VNCAuth{Password: "x", Rand: bytes.NewReader(nil)}).Authenticate(&buf); !errors.Is(err, io.EOF) {
		t.Fatalf("err %v, want the source's EOF", err)
	}
}

func TestSecurityTypeNumbers(t *testing.T) {
	if (NoSecurity{}).Type() != 1 || (VNCAuth{}).Type() != 2 || SecurityVNCAuth != 2 {
		t.Fatal("security type numbers changed")
	}
	if err := (NoSecurity{}).Authenticate(nil); err != nil {
		t.Fatal(err)
	}
}
