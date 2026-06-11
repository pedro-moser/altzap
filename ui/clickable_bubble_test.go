package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

func TestClickableBubble_MiddleButtonInvokesCallback(t *testing.T) {
	calls := 0
	cb := newClickableBubble(widget.NewLabel("hi"), func() { calls++ }, nil, nil)

	cb.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonTertiary})

	if calls != 1 {
		t.Fatalf("middle click did not invoke callback exactly once, got %d", calls)
	}
}

func TestClickableBubble_PrimaryButtonIgnored(t *testing.T) {
	calls := 0
	cb := newClickableBubble(widget.NewLabel("hi"), func() { calls++ }, nil, nil)

	cb.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	cb.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonSecondary})

	if calls != 0 {
		t.Fatalf("non-middle clicks must not invoke callback, got %d", calls)
	}
}

func TestClickableBubble_NilCallbackSafe(t *testing.T) {
	cb := newClickableBubble(widget.NewLabel("hi"), nil, nil, nil)
	// Must not panic.
	cb.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonTertiary})
	cb.DoubleTapped(nil)
	cb.TappedSecondary(nil)
}

func TestClickableBubble_SecondaryTapForwardsEvent(t *testing.T) {
	var got *fyne.PointEvent
	cb := newClickableBubble(widget.NewLabel("hi"), nil, nil, func(e *fyne.PointEvent) { got = e })

	ev := &fyne.PointEvent{AbsolutePosition: fyne.NewPos(12, 34)}
	cb.TappedSecondary(ev)

	if got != ev {
		t.Fatalf("secondary tap must forward the original event, got %+v", got)
	}
}

func TestClickableBubble_NilEventSafe(t *testing.T) {
	calls := 0
	cb := newClickableBubble(widget.NewLabel("hi"), func() { calls++ }, nil, nil)
	cb.MouseDown(nil)
	if calls != 0 {
		t.Fatalf("nil event must not invoke callback")
	}
}

func TestClickableBubble_DoubleTapInvokesReplyCallback(t *testing.T) {
	calls := 0
	cb := newClickableBubble(widget.NewLabel("hi"), nil, func() { calls++ }, nil)

	cb.DoubleTapped(nil)

	if calls != 1 {
		t.Fatalf("double tap did not invoke reply callback exactly once, got %d", calls)
	}
}

// Compile-time checks that clickableBubble satisfies every input
// interface we rely on — protects against accidental signature drift
// and against Fyne's hit-test silently dropping events because the
// "deepest match" widget didn't implement the right interface.
var (
	_ desktop.Mouseable      = (*clickableBubble)(nil)
	_ fyne.Tappable          = (*clickableBubble)(nil)
	_ fyne.DoubleTappable    = (*clickableBubble)(nil)
	_ fyne.SecondaryTappable = (*clickableBubble)(nil)
)
