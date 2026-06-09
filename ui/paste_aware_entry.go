package ui

import (
	"sync"

	"altzap/client"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// pasteAwareEntry is the message composer that intercepts Ctrl+V to
// route image-bearing clipboards into the send-image dialog and lets
// app-level CustomShortcuts (Ctrl+J/K/F/R) bubble through to their
// canvas-registered handlers even when the entry has keyboard focus.
//
// Why the second responsibility lives here:  Fyne dispatches shortcuts
// to a focused fyne.Shortcutable widget exclusively (see
// internal/driver/glfw/window.go::processKey). widget.Entry IS
// Shortcutable but only registers Cut/Copy/Paste/SelectAll/Undo/Redo +
// arrow-key chord handlers — every other shortcut hits its handler and
// silently no-ops. The result is that custom Ctrl+J, Ctrl+K, Ctrl+F,
// etc. registered at canvas level disappear whenever the composer
// holds focus. We work around it by mirroring the registration here:
// AddCustomShortcut stores the same handler on the entry, and the
// overridden TypedShortcut runs it before falling through.
//
// The image-detection function and the custom-shortcut map are both
// injectable so tests can exercise dispatch without spinning up a
// real Fyne app or subprocess.
type pasteAwareEntry struct {
	widget.Entry
	tryImagePaste func() (string, error) // typically client.PasteImageFromClipboard
	onImagePasted func(path string)      // called with the persisted tmp path

	// OnSendRequested fires on Enter without Shift. The composer is a
	// multi-line Entry, so widget.Entry's OnSubmitted (single-line only)
	// never triggers — this is the keyboard send path. Shift+Enter falls
	// through to the embedded Entry and inserts a newline.
	OnSendRequested func()
	shiftHeld       bool // touched only on the UI thread (KeyDown/KeyUp/TypedKey)

	customMu        sync.RWMutex
	customShortcuts map[customShortcutKey]func()
}

// customShortcutKey identifies a desktop CustomShortcut by its
// (KeyName, Modifier) pair — the only fields that uniquely describe
// a Ctrl+letter binding. Used as a map key inside pasteAwareEntry.
type customShortcutKey struct {
	KeyName  fyne.KeyName
	Modifier fyne.KeyModifier
}

// newPasteAwareEntry returns a fresh entry. tryImagePaste defaults to
// client.PasteImageFromClipboard when nil; onImagePasted may be nil
// (image paste then becomes a no-op and the default text paste runs).
func newPasteAwareEntry(tryImagePaste func() (string, error), onImagePasted func(path string)) *pasteAwareEntry {
	if tryImagePaste == nil {
		tryImagePaste = client.PasteImageFromClipboard
	}
	e := &pasteAwareEntry{
		tryImagePaste: tryImagePaste,
		onImagePasted: onImagePasted,
	}
	// Multi-line so Shift+Enter can break lines. The 1-row baseline height
	// is applied by inputBarBuild — SetMinRowsVisible refreshes the widget,
	// which needs a running Fyne app (headless tests construct this entry).
	e.MultiLine = true
	e.Wrapping = fyne.TextWrapWord
	e.ExtendBaseWidget(e)
	return e
}

// tryHandlePasteAsImage runs the image-detection / callback step in
// isolation from any Fyne-context-dependent default text paste. Returns
// true when the shortcut was an image paste and the callback fired,
// telling the caller to skip default delegation. Extracted so tests can
// verify the discriminator without spinning up a Fyne app.
func (e *pasteAwareEntry) tryHandlePasteAsImage(s fyne.Shortcut) bool {
	if _, isPaste := s.(*fyne.ShortcutPaste); !isPaste {
		return false
	}
	if e.onImagePasted == nil || e.tryImagePaste == nil {
		return false
	}
	path, err := e.tryImagePaste()
	if err != nil || path == "" {
		return false
	}
	e.onImagePasted(path)
	return true
}

// AddCustomShortcut mirrors a desktop.CustomShortcut + handler from
// the canvas onto this entry, so the same chord works whether the
// composer is focused (entry intercepts) or not (canvas dispatches).
// Safe to call from any goroutine.
func (e *pasteAwareEntry) AddCustomShortcut(shortcut *desktop.CustomShortcut, handler func()) {
	if shortcut == nil || handler == nil {
		return
	}
	e.customMu.Lock()
	defer e.customMu.Unlock()
	if e.customShortcuts == nil {
		e.customShortcuts = make(map[customShortcutKey]func())
	}
	e.customShortcuts[customShortcutKey{
		KeyName:  shortcut.KeyName,
		Modifier: shortcut.Modifier,
	}] = handler
}

// dispatchCustomShortcut returns true and runs the registered handler
// for s when s matches one of our mirrored CustomShortcuts; otherwise
// returns false so the caller knows to delegate to the embedded Entry.
// Extracted so tests can verify dispatch without touching Fyne's
// TypedShortcut runtime path.
func (e *pasteAwareEntry) dispatchCustomShortcut(s fyne.Shortcut) bool {
	cs, ok := s.(*desktop.CustomShortcut)
	if !ok {
		return false
	}
	e.customMu.RLock()
	handler := e.customShortcuts[customShortcutKey{
		KeyName:  cs.KeyName,
		Modifier: cs.Modifier,
	}]
	e.customMu.RUnlock()
	if handler == nil {
		return false
	}
	handler()
	return true
}

// TypedShortcut overrides the embedded Entry's handler so we can:
//  1. Treat Ctrl+V as image paste when the clipboard holds an image
//     (else fall through to text paste).
//  2. Run any mirrored CustomShortcut (Ctrl+J/K/F/R/L) that the
//     embedded Entry would otherwise swallow silently.
//
// Anything else passes straight through to widget.Entry.
func (e *pasteAwareEntry) TypedShortcut(s fyne.Shortcut) {
	if e.tryHandlePasteAsImage(s) {
		return
	}
	if e.dispatchCustomShortcut(s) {
		return
	}
	e.Entry.TypedShortcut(s)
}

// TypedKey routes the Escape key to the app-wide ESC stack (see
// ui/esc.go) before delegating to the embedded Entry. widget.Entry's
// default TypedKey switch returns silently on Escape, so when the
// composer is focused the chat view's reply-mode/search dismiss
// handlers would otherwise never fire. Forwarding here keeps ESC
// dismissal consistent regardless of which widget holds focus.
//
// Enter (without Shift) is the send gesture; Shift+Enter delegates to
// the embedded multi-line Entry, which inserts the newline.
func (e *pasteAwareEntry) TypedKey(key *fyne.KeyEvent) {
	if key != nil {
		switch key.Name {
		case fyne.KeyEscape:
			handleEsc()
			return
		case fyne.KeyReturn, fyne.KeyEnter:
			if !e.shiftHeld && e.OnSendRequested != nil {
				e.OnSendRequested()
				return
			}
		}
	}
	e.Entry.TypedKey(key)
}

// KeyDown / KeyUp track the physical Shift state so TypedKey can tell
// Enter (send) from Shift+Enter (newline). Both delegate to the embedded
// Entry, which consumes the same events for shift-selection.
func (e *pasteAwareEntry) KeyDown(key *fyne.KeyEvent) {
	if key != nil && (key.Name == desktop.KeyShiftLeft || key.Name == desktop.KeyShiftRight) {
		e.shiftHeld = true
	}
	e.Entry.KeyDown(key)
}

func (e *pasteAwareEntry) KeyUp(key *fyne.KeyEvent) {
	if key != nil && (key.Name == desktop.KeyShiftLeft || key.Name == desktop.KeyShiftRight) {
		e.shiftHeld = false
	}
	e.Entry.KeyUp(key)
}

// escAwareEntry is a lightweight widget.Entry used for the sidebar search
// field. It forwards Escape to the ESC stack instead of just unfocusing and
// can mirror app-level CustomShortcuts so Ctrl+J/K/F/R/L still work while the
// search field has keyboard focus.
type escAwareEntry struct {
	widget.Entry
	customMu        sync.RWMutex
	customShortcuts map[customShortcutKey]func()
}

func newEscAwareEntry() *escAwareEntry {
	e := &escAwareEntry{}
	e.ExtendBaseWidget(e)
	return e
}

func (e *escAwareEntry) AddCustomShortcut(shortcut *desktop.CustomShortcut, handler func()) {
	if shortcut == nil || handler == nil {
		return
	}
	e.customMu.Lock()
	defer e.customMu.Unlock()
	if e.customShortcuts == nil {
		e.customShortcuts = make(map[customShortcutKey]func())
	}
	e.customShortcuts[customShortcutKey{
		KeyName:  shortcut.KeyName,
		Modifier: shortcut.Modifier,
	}] = handler
}

func (e *escAwareEntry) dispatchCustomShortcut(s fyne.Shortcut) bool {
	cs, ok := s.(*desktop.CustomShortcut)
	if !ok {
		return false
	}
	e.customMu.RLock()
	handler := e.customShortcuts[customShortcutKey{
		KeyName:  cs.KeyName,
		Modifier: cs.Modifier,
	}]
	e.customMu.RUnlock()
	if handler == nil {
		return false
	}
	handler()
	return true
}

func (e *escAwareEntry) TypedShortcut(s fyne.Shortcut) {
	if e.dispatchCustomShortcut(s) {
		return
	}
	e.Entry.TypedShortcut(s)
}

func (e *escAwareEntry) TypedKey(key *fyne.KeyEvent) {
	if key != nil && key.Name == fyne.KeyEscape {
		handleEsc()
		return
	}
	e.Entry.TypedKey(key)
}
