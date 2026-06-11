package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

func TestTappableBox_TappedFiresCallback(t *testing.T) {
	calls := 0
	tb := newTappableBox(widget.NewLabel("x"), func() { calls++ })

	tb.Tapped(nil)

	if calls != 1 {
		t.Fatalf("Tapped should fire onTap exactly once, got %d", calls)
	}
}

func TestTappableBox_NilCallbackSafe(t *testing.T) {
	tb := newTappableBox(widget.NewLabel("x"), nil)
	tb.Tapped(nil)          // must not panic
	tb.TappedSecondary(nil) // ditto, with no secondary handler wired
}

func TestTappableBox_SecondaryTapForwardsEvent(t *testing.T) {
	var got *fyne.PointEvent
	tb := newTappableBox(widget.NewLabel("x"), nil)
	tb.SetOnSecondary(func(e *fyne.PointEvent) { got = e })

	ev := &fyne.PointEvent{AbsolutePosition: fyne.NewPos(7, 9)}
	tb.TappedSecondary(ev)

	if got != ev {
		t.Fatalf("secondary tap must forward the original event, got %+v", got)
	}
}

func TestTappableBox_CursorIsPointer(t *testing.T) {
	tb := newTappableBox(widget.NewLabel("x"), func() {})
	if got := tb.Cursor(); got != desktop.PointerCursor {
		t.Fatalf("want PointerCursor, got %v", got)
	}
}

// Compile-time check that tappableBox satisfies fyne.Tappable + the
// Cursorable interface — guards against signature drift.
var (
	_ fyne.Tappable          = (*tappableBox)(nil)
	_ fyne.SecondaryTappable = (*tappableBox)(nil)
	_ desktop.Cursorable     = (*tappableBox)(nil)
)
