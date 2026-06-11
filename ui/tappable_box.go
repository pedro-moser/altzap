package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// tappableBox wraps a non-interactive CanvasObject (e.g. the reply
// quote preview at the top of a bubble) and turns left clicks into a
// callback. Cursor() returns PointerCursor so hovering hints that the
// region is clickable. Distinct from clickableBubble — that one
// captures middle-click for copy and double-click for reply on the
// message body; this one only cares about a single tap on a
// decorative element. Two widgets stay simpler than overloading
// clickableBubble with another callback slot we'd only ever exercise
// from one call site.
type tappableBox struct {
	widget.BaseWidget
	content     fyne.CanvasObject
	onTap       func()
	onSecondary func(*fyne.PointEvent)
}

func newTappableBox(content fyne.CanvasObject, onTap func()) *tappableBox {
	t := &tappableBox{content: content, onTap: onTap}
	t.ExtendBaseWidget(t)
	return t
}

func (t *tappableBox) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(t.content)
}

// Tapped fires onTap on a left-click. Nil callback is a no-op so a
// stray newTappableBox(content, nil) doesn't crash.
func (t *tappableBox) Tapped(*fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap()
	}
}

// SetOnSecondary wires an optional right-click handler. Without it the
// box swallows the event silently — Fyne's hit-test stops at the deepest
// matching widget, so a right-click over the quote preview would never
// reach the bubble's context-menu wrapper underneath.
func (t *tappableBox) SetOnSecondary(fn func(*fyne.PointEvent)) {
	t.onSecondary = fn
}

// TappedSecondary forwards right-clicks to the optional handler.
func (t *tappableBox) TappedSecondary(e *fyne.PointEvent) {
	if t.onSecondary != nil {
		t.onSecondary(e)
	}
}

// Cursor implements desktop.Cursorable so the mouse pointer turns
// into the "click me" hand when hovering over the box. Visual cue
// matters here — without it, users can't distinguish the
// tap-to-jump reply preview from a static label.
func (t *tappableBox) Cursor() desktop.Cursor {
	return desktop.PointerCursor
}
