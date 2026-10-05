package govnc

import (
	"image/color"
	"log/slog"
	"testing"
	"time"

	"github.com/anchorbrowser/govnc/rfb"
)

func TestWithDefaults(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		wantErr bool
		check   func(*testing.T, Options)
	}{
		{
			name: "all defaults",
			opts: Options{},
			check: func(t *testing.T, o Options) {
				eq(t, DefaultName, o.Name)
				eq(t, DefaultHandshakeTimeout, o.HandshakeTimeout)
				eq(t, DefaultWriteTimeout, o.WriteTimeout)
				if o.Logger == nil {
					t.Fatal("logger must not be nil after withDefaults")
				}
			},
		},
		{
			name: "preserves non-zero values",
			opts: Options{
				Name:             "custom",
				HandshakeTimeout: 5 * time.Second,
				WriteTimeout:     3 * time.Second,
			},
			check: func(t *testing.T, o Options) {
				eq(t, "custom", o.Name)
				eq(t, 5*time.Second, o.HandshakeTimeout)
				eq(t, 3*time.Second, o.WriteTimeout)
			},
		},
		{
			name:    "negative MaxSessions",
			opts:    Options{MaxSessions: -1},
			wantErr: true,
		},
		{
			name:    "nil security entry",
			opts:    Options{Security: []rfb.SecurityType{nil}},
			wantErr: true,
		},
		{
			name:    "VNCAuth value without password",
			opts:    Options{Security: []rfb.SecurityType{rfb.VNCAuth{}}},
			wantErr: true,
		},
		{
			name:    "VNCAuth pointer without password",
			opts:    Options{Security: []rfb.SecurityType{&rfb.VNCAuth{}}},
			wantErr: true,
		},
		{
			name:    "VNCAuth pointer nil",
			opts:    Options{Security: []rfb.SecurityType{(*rfb.VNCAuth)(nil)}},
			wantErr: true,
		},
		{
			name: "VNCAuth with password",
			opts: Options{Security: []rfb.SecurityType{rfb.VNCAuth{Password: "secret"}}},
			check: func(t *testing.T, o Options) {
				if len(o.Security) != 1 {
					t.Fatal("security list length changed")
				}
			},
		},
		{
			name: "VNCAuth pointer with password",
			opts: Options{Security: []rfb.SecurityType{&rfb.VNCAuth{Password: "secret"}}},
			check: func(t *testing.T, o Options) {
				if len(o.Security) != 1 {
					t.Fatal("security list length changed")
				}
			},
		},
		{
			name:    "cursor without image",
			opts:    Options{Cursor: &Cursor{}},
			wantErr: true,
		},
		{
			name: "cursor hotspot outside X",
			opts: Options{Cursor: &Cursor{
				Image: solid(2, 2, color.RGBA{A: 255}),
				HotX:  2,
			}},
			wantErr: true,
		},
		{
			name: "cursor hotspot outside Y",
			opts: Options{Cursor: &Cursor{
				Image: solid(2, 2, color.RGBA{A: 255}),
				HotY:  2,
			}},
			wantErr: true,
		},
		{
			name: "valid cursor",
			opts: Options{Cursor: &Cursor{
				Image: solid(2, 2, color.RGBA{A: 255}),
				HotX:  1,
				HotY:  1,
			}},
			check: func(t *testing.T, o Options) {
				if o.Cursor == nil {
					t.Fatal("cursor was lost")
				}
			},
		},
		{
			name: "zero MaxSessions is valid",
			opts: Options{MaxSessions: 0},
			check: func(t *testing.T, o Options) {
				eq(t, 0, o.MaxSessions)
			},
		},
		{
			name: "custom logger preserved",
			opts: Options{Logger: slog.New(slog.NewTextHandler(nil, nil))},
			check: func(t *testing.T, o Options) {
				if o.Logger == nil {
					t.Fatal("custom logger was lost")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.opts.withDefaults()
			if tt.wantErr {
				if err == nil {
					t.Fatal("want an error")
				}
				return
			}
			noErr(t, err)
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}
