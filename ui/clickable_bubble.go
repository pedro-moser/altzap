package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// clickableBubble wraps a bubble's visible content and intercepts mouse
// gestures the bubble cares about: middle-click for copy, double-click
// for reply and right-click for the context menu. Implements Tappable so
// left-clicks don't fall through to some surprise inner widget; the
// single-tap callback is a no-op by default but can be wired for future
// selection behavior.
type clickableBubble struct {
	widget.BaseWidget
	content       fyne.CanvasObject
	onMiddleClick func()
	onDoubleClick func()
	onSecondary   func(*fyne.PointEvent)
}

// newClickableBubble wraps content with mouse-event capture. Any
// callback may be nil; the corresponding event becomes a no-op.
func newClickableBubble(content fyne.CanvasObject, onMiddleClick, onDoubleClick func(), onSecondary func(*fyne.PointEvent)) *clickableBubble {
	cb := &clickableBubble{
		content:       content,
		onMiddleClick: onMiddleClick,
		onDoubleClick: onDoubleClick,
		onSecondary:   onSecondary,
	}
	cb.ExtendBaseWidget(cb)
	return cb
}

func (cb *clickableBubble) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(cb.content)
}

// MouseDown is called by the desktop driver for any mouse press over the
// widget. We only act on the tertiary (middle) button; primary/secondary
// fall through so the wrapped content's own handlers (text selection
// later, hyperlink taps now) keep working.
func (cb *clickableBubble) MouseDown(e *desktop.MouseEvent) {
	if e == nil || e.Button != desktop.MouseButtonTertiary {
		return
	}
	if cb.onMiddleClick != nil {
		cb.onMiddleClick()
	}
}

// MouseUp completes the desktop.Mouseable contract; we don't need the
// release signal but Fyne expects both methods on the interface.
func (cb *clickableBubble) MouseUp(*desktop.MouseEvent) {}

// Tapped is a no-op. It exists so we satisfy fyne.Tappable — Fyne's
// dispatch otherwise would mark a left-click on the wrapper as
// "unhandled" and bubble it past us, occasionally re-triggering the
// surrounding widget.List's row-selection in surprising ways.
func (cb *clickableBubble) Tapped(*fyne.PointEvent) {}

// DoubleTapped fires on a double left-click; mapped to the reply
// gesture (mirroring the WhatsApp Web idiom of "double-click to quote").
func (cb *clickableBubble) DoubleTapped(*fyne.PointEvent) {
	if cb.onDoubleClick != nil {
		cb.onDoubleClick()
	}
}

// TappedSecondary fires on right-click; mapped to the per-message context
// menu. The event is forwarded so the handler can anchor the menu at the
// click's AbsolutePosition (canvas coordinates).
func (cb *clickableBubble) TappedSecondary(e *fyne.PointEvent) {
	if cb.onSecondary != nil {
		cb.onSecondary(e)
	}
}
