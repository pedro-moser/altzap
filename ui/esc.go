package ui

import (
	"sync"

	"fyne.io/fyne/v2"
)

// ESC dispatch is centralized: every dismissible overlay or transient
// state (image popup, video popup, reply preview, modal notice, …)
// pushes a dismiss callback on a single stack. A Canvas TypedKey handler
// installed once at startup pops + invokes the top of the stack on ESC.
//
// Why a stack: Fyne's `canvas.SetOnTypedKey` accepts a single handler; if
// every popup tried to register its own with save/restore semantics,
// nested overlays (popup-on-popup) would race each other and we'd need
// per-popup bookkeeping anyway. Centralizing keeps each overlay's job to
// "push my dismiss; pop when I'm gone" — same idiom across the codebase.
//
// Each push gets a unique id so pop can find the right entry even if
// other entries pushed/popped in between.

type escEntry struct {
	id      uint64
	dismiss func()
}

var (
	escMu     sync.Mutex
	escStack  []escEntry
	escNextID uint64
)

// pushEsc registers a dismiss callback at the top of the ESC stack.
// Returns a pop function that removes the entry from the stack — call
// it from any other dismissal path (Close button, programmatic cancel)
// so a stale dismiss doesn't fire on the next ESC.
func pushEsc(dismiss func()) (pop func()) {
	escMu.Lock()
	escNextID++
	id := escNextID
	escStack = append(escStack, escEntry{id: id, dismiss: dismiss})
	escMu.Unlock()
	return func() {
		escMu.Lock()
		for i, e := range escStack {
			if e.id == id {
				escStack = append(escStack[:i], escStack[i+1:]...)
				break
			}
		}
		escMu.Unlock()
	}
}

// handleEsc invokes (and removes) the top dismiss callback on the stack.
// No-op if the stack is empty.
func handleEsc() {
	escMu.Lock()
	n := len(escStack)
	if n == 0 {
		escMu.Unlock()
		return
	}
	top := escStack[n-1]
	escStack = escStack[:n-1]
	escMu.Unlock()
	if top.dismiss != nil {
		top.dismiss()
	}
}

// InstallEscHandler wires the canvas's TypedKey to dispatch ESC into the
// stack. Call once at app start with the main window's canvas. The app
// doesn't use SetOnTypedKey for anything else, so we don't need to
// chain to a previous handler.
func InstallEscHandler(c fyne.Canvas) {
	if c == nil {
		return
	}
	c.SetOnTypedKey(func(ev *fyne.KeyEvent) {
		if ev.Name == fyne.KeyEscape {
			handleEsc()
		}
	})
}
