package govnc

import (
	"context"
	"testing"

	"github.com/anchorbrowser/govnc/rfb"
)

func TestHooksHandleKeyStopsAtFirstTrue(t *testing.T) {
	called := []int{}
	h := Hooks{
		Key: []KeyHandler{
			func(_ context.Context, ev rfb.KeyEvent) bool { called = append(called, 1); return false },
			func(_ context.Context, ev rfb.KeyEvent) bool { called = append(called, 2); return true },
			func(_ context.Context, ev rfb.KeyEvent) bool { called = append(called, 3); return true },
		},
	}

	got := h.HandleKey(context.Background(), rfb.KeyEvent{Keysym: 'a'})
	eq(t, true, got)
	eq(t, []int{1, 2}, called)
}

func TestHooksHandleKeyRunsAllOnFalse(t *testing.T) {
	called := []int{}
	h := Hooks{
		Key: []KeyHandler{
			func(_ context.Context, ev rfb.KeyEvent) bool { called = append(called, 1); return false },
			func(_ context.Context, ev rfb.KeyEvent) bool { called = append(called, 2); return false },
			func(_ context.Context, ev rfb.KeyEvent) bool { called = append(called, 3); return false },
		},
	}

	got := h.HandleKey(context.Background(), rfb.KeyEvent{Keysym: 'a'})
	eq(t, false, got)
	eq(t, []int{1, 2, 3}, called)
}

func TestHooksHandleKeyEmptyReturnsFalse(t *testing.T) {
	h := Hooks{}
	got := h.HandleKey(context.Background(), rfb.KeyEvent{Keysym: 'a'})
	eq(t, false, got)
}

func TestHooksHandleKeyPassesContext(t *testing.T) {
	type ctxKey string
	key := ctxKey("test")
	ctx := context.WithValue(context.Background(), key, "value")

	var gotCtx context.Context
	h := Hooks{
		Key: []KeyHandler{
			func(c context.Context, ev rfb.KeyEvent) bool {
				gotCtx = c
				return true
			},
		},
	}

	h.HandleKey(ctx, rfb.KeyEvent{})
	if gotCtx.Value(key) != "value" {
		t.Fatal("context not passed through")
	}
}

func TestHooksHandlePointerStopsAtFirstTrue(t *testing.T) {
	called := []int{}
	h := Hooks{
		Pointer: []PointerHandler{
			func(_ context.Context, ev rfb.PointerEvent) bool { called = append(called, 1); return false },
			func(_ context.Context, ev rfb.PointerEvent) bool { called = append(called, 2); return true },
			func(_ context.Context, ev rfb.PointerEvent) bool { called = append(called, 3); return true },
		},
	}

	got := h.HandlePointer(context.Background(), rfb.PointerEvent{X: 10, Y: 20})
	eq(t, true, got)
	eq(t, []int{1, 2}, called)
}

func TestHooksHandlePointerRunsAllOnFalse(t *testing.T) {
	called := []int{}
	h := Hooks{
		Pointer: []PointerHandler{
			func(_ context.Context, ev rfb.PointerEvent) bool { called = append(called, 1); return false },
			func(_ context.Context, ev rfb.PointerEvent) bool { called = append(called, 2); return false },
			func(_ context.Context, ev rfb.PointerEvent) bool { called = append(called, 3); return false },
		},
	}

	got := h.HandlePointer(context.Background(), rfb.PointerEvent{})
	eq(t, false, got)
	eq(t, []int{1, 2, 3}, called)
}

func TestHooksHandlePointerEmptyReturnsFalse(t *testing.T) {
	h := Hooks{}
	got := h.HandlePointer(context.Background(), rfb.PointerEvent{})
	eq(t, false, got)
}

func TestHooksHandlePointerPassesContext(t *testing.T) {
	type ctxKey string
	key := ctxKey("test")
	ctx := context.WithValue(context.Background(), key, "pointer")

	var gotCtx context.Context
	h := Hooks{
		Pointer: []PointerHandler{
			func(c context.Context, ev rfb.PointerEvent) bool {
				gotCtx = c
				return true
			},
		},
	}

	h.HandlePointer(ctx, rfb.PointerEvent{})
	if gotCtx.Value(key) != "pointer" {
		t.Fatal("context not passed through")
	}
}

func TestHooksHandleClipboardStopsAtFirstTrue(t *testing.T) {
	called := []int{}
	h := Hooks{
		Clipboard: []ClipboardHandler{
			func(_ context.Context, text string) bool { called = append(called, 1); return false },
			func(_ context.Context, text string) bool { called = append(called, 2); return true },
			func(_ context.Context, text string) bool { called = append(called, 3); return true },
		},
	}

	got := h.HandleClipboard(context.Background(), "test")
	eq(t, true, got)
	eq(t, []int{1, 2}, called)
}

func TestHooksHandleClipboardRunsAllOnFalse(t *testing.T) {
	called := []int{}
	h := Hooks{
		Clipboard: []ClipboardHandler{
			func(_ context.Context, text string) bool { called = append(called, 1); return false },
			func(_ context.Context, text string) bool { called = append(called, 2); return false },
			func(_ context.Context, text string) bool { called = append(called, 3); return false },
		},
	}

	got := h.HandleClipboard(context.Background(), "test")
	eq(t, false, got)
	eq(t, []int{1, 2, 3}, called)
}

func TestHooksHandleClipboardEmptyReturnsFalse(t *testing.T) {
	h := Hooks{}
	got := h.HandleClipboard(context.Background(), "test")
	eq(t, false, got)
}

func TestHooksHandleClipboardPassesContext(t *testing.T) {
	type ctxKey string
	key := ctxKey("test")
	ctx := context.WithValue(context.Background(), key, "clipboard")

	var gotCtx context.Context
	h := Hooks{
		Clipboard: []ClipboardHandler{
			func(c context.Context, text string) bool {
				gotCtx = c
				return true
			},
		},
	}

	h.HandleClipboard(ctx, "test")
	if gotCtx.Value(key) != "clipboard" {
		t.Fatal("context not passed through")
	}
}

func TestHooksHandleClipboardReceivesText(t *testing.T) {
	var gotText string
	h := Hooks{
		Clipboard: []ClipboardHandler{
			func(_ context.Context, text string) bool {
				gotText = text
				return true
			},
		},
	}

	h.HandleClipboard(context.Background(), "hello world")
	eq(t, "hello world", gotText)
}
