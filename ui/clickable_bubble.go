package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// clickableBubble wraps a bubble's visible content and intercepts middle-
// mouse clicks (the WhatsApp-Web idiom for "copy this message"). All other
// pointer events fall through to the wrapped content via the renderer.
type clickableBubble struct {
	widget.BaseWidget
	content       fyne.CanvasObject
	onMiddleClick func()
}

// newClickableBubble wraps content so that middle-clicking it invokes
// onMiddleClick. Pass nil onMiddleClick to skip handling (still wraps —
// useful for keeping the widget tree shape uniform across deleted vs
// regular bubbles).
func newClickableBubble(content fyne.CanvasObject, onMiddleClick func()) *clickableBubble {
	cb := &clickableBubble{
		content:       content,
		onMiddleClick: onMiddleClick,
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
