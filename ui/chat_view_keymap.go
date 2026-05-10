package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

// installShortcuts registers all keyboard navigation handlers on the
// window canvas. Idempotent against being called once per ChatView
// rebuild (the canvas itself is per-window, but Build() runs at most
// once for the chat surface).
//
// The Ctrl+J/K handlers branch on cv.replyMode at dispatch time — same
// shortcut, different action depending on whether reply mode is armed.
//
// Canvas.OnTypedRune is co-opted for reply mode's "any printable key
// confirms" behavior: when reply mode is active and no widget owns
// keyboard focus (enterReplyMode parks focus precisely so this handler
// fires), any typed rune confirms the target. Outside reply mode the
// handler defers to a captured prior callback so existing TypedRune
// behavior is preserved.
func (cv *ChatView) installShortcuts() {
	if cv.window == nil {
		return
	}
	canvas := cv.window.Canvas()

	register := func(key fyne.KeyName, handler func()) {
		shortcut := &desktop.CustomShortcut{
			KeyName:  key,
			Modifier: fyne.KeyModifierControl,
		}
		canvas.AddShortcut(shortcut, func(_ fyne.Shortcut) { handler() })
		// Mirror onto the composer so the chord still fires when the
		// entry has focus — widget.Entry is fyne.Shortcutable and Fyne
		// dispatches focused-widget shortcuts exclusively, silently
		// dropping anything Entry doesn't itself recognize.
		if cv.messageInput != nil {
			cv.messageInput.AddCustomShortcut(shortcut, handler)
		}
	}

	register(fyne.KeyJ, func() {
		if cv.replyMode {
			cv.replyTargetNext()
			return
		}
		cv.selectNextChat()
	})
	register(fyne.KeyK, func() {
		if cv.replyMode {
			cv.replyTargetPrev()
			return
		}
		cv.selectPrevChat()
	})
	register(fyne.KeyL, func() { cv.focusComposer() })
	register(fyne.KeyF, func() { cv.openSearch() })
	register(fyne.KeyR, func() { cv.enterReplyMode() })

	// Reply-mode "any printable confirms": enterReplyMode parks the
	// previous focus so typed runes reach Canvas.OnTypedRune instead of
	// any focused widget. We chain to whatever rune handler was already
	// installed (currently none, but defensive).
	prev := cv.priorTypedRune
	canvas.SetOnTypedRune(func(r rune) {
		if cv.replyMode {
			cv.confirmReplyTarget()
			return
		}
		if prev != nil {
			prev(r)
		}
	})
}
