package ui

import (
	"testing"

	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

func TestClickableBubble_MiddleButtonInvokesCallback(t *testing.T) {
	calls := 0
	cb := newClickableBubble(widget.NewLabel("hi"), func() { calls++ })

	cb.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonTertiary})

	if calls != 1 {
		t.Fatalf("middle click did not invoke callback exactly once, got %d", calls)
	}
}

func TestClickableBubble_PrimaryButtonIgnored(t *testing.T) {
	calls := 0
	cb := newClickableBubble(widget.NewLabel("hi"), func() { calls++ })

	cb.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	cb.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonSecondary})

	if calls != 0 {
		t.Fatalf("non-middle clicks must not invoke callback, got %d", calls)
	}
}

func TestClickableBubble_NilCallbackSafe(t *testing.T) {
	cb := newClickableBubble(widget.NewLabel("hi"), nil)
	// Must not panic.
	cb.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonTertiary})
}

func TestClickableBubble_NilEventSafe(t *testing.T) {
	calls := 0
	cb := newClickableBubble(widget.NewLabel("hi"), func() { calls++ })
	cb.MouseDown(nil)
	if calls != 0 {
		t.Fatalf("nil event must not invoke callback")
	}
}

// Compile-time check that clickableBubble satisfies the Mouseable
// interface — protects against accidental signature drift.
var _ desktop.Mouseable = (*clickableBubble)(nil)
