package ui

import (
	"errors"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

func TestPasteAwareEntry_ImagePasteFiresCallback(t *testing.T) {
	pasted := ""
	stub := func() (string, error) { return "/tmp/altzap-paste-fake.png", nil }
	e := newPasteAwareEntry(stub, func(path string) { pasted = path })

	if !e.tryHandlePasteAsImage(&fyne.ShortcutPaste{}) {
		t.Fatal("image paste should have been claimed (returned true)")
	}
	if pasted != "/tmp/altzap-paste-fake.png" {
		t.Fatalf("onImagePasted not called with the right path, got %q", pasted)
	}
}

func TestPasteAwareEntry_NoImageDoesNotConsumeShortcut(t *testing.T) {
	calls := 0
	stub := func() (string, error) { return "", errors.New("no image in clipboard") }
	e := newPasteAwareEntry(stub, func(string) { calls++ })

	if e.tryHandlePasteAsImage(&fyne.ShortcutPaste{}) {
		t.Fatal("paste with no image must not be claimed (caller falls through to text)")
	}
	if calls != 0 {
		t.Fatalf("image callback fired even though clipboard had no image, calls=%d", calls)
	}
}

func TestPasteAwareEntry_EmptyPathDoesNotConsumeShortcut(t *testing.T) {
	stub := func() (string, error) { return "", nil }
	e := newPasteAwareEntry(stub, func(string) {})
	if e.tryHandlePasteAsImage(&fyne.ShortcutPaste{}) {
		t.Fatal("empty path must not be treated as a successful image paste")
	}
}

func TestPasteAwareEntry_NonPasteShortcutPassesThrough(t *testing.T) {
	calls := 0
	stub := func() (string, error) {
		calls++
		return "/tmp/x.png", nil
	}
	e := newPasteAwareEntry(stub, func(string) {})

	if e.tryHandlePasteAsImage(&fyne.ShortcutCopy{}) {
		t.Fatal("copy shortcut should not be treated as paste")
	}
	if calls != 0 {
		t.Fatalf("non-paste shortcut should not invoke image fetcher, calls=%d", calls)
	}
}

func TestPasteAwareEntry_NilCallbacksAreNoOp(t *testing.T) {
	// nil onImagePasted means we never want to handle the image case;
	// the discriminator should bail out and the caller delegates.
	e := newPasteAwareEntry(nil, nil)
	if e.tryHandlePasteAsImage(&fyne.ShortcutPaste{}) {
		t.Fatal("nil onImagePasted should never claim the shortcut")
	}
}

func TestPasteAwareEntry_AddCustomShortcutDispatches(t *testing.T) {
	e := newPasteAwareEntry(nil, nil)
	calls := 0
	sc := &desktop.CustomShortcut{KeyName: fyne.KeyJ, Modifier: fyne.KeyModifierControl}
	e.AddCustomShortcut(sc, func() { calls++ })

	if !e.dispatchCustomShortcut(sc) {
		t.Fatal("registered shortcut should be claimed by dispatchCustomShortcut")
	}
	if calls != 1 {
		t.Fatalf("handler should fire exactly once, got %d", calls)
	}
}

func TestPasteAwareEntry_DispatchUnknownShortcutFallsThrough(t *testing.T) {
	e := newPasteAwareEntry(nil, nil)
	// no shortcuts registered
	if e.dispatchCustomShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyZ, Modifier: fyne.KeyModifierControl}) {
		t.Fatal("unknown shortcut must fall through (return false)")
	}
}

func TestPasteAwareEntry_DispatchIgnoresNonCustomShortcut(t *testing.T) {
	e := newPasteAwareEntry(nil, nil)
	// Even with a custom shortcut registered, a built-in fyne shortcut
	// (e.g. Copy) must not match — the type assertion guards us.
	e.AddCustomShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyJ, Modifier: fyne.KeyModifierControl}, func() {})
	if e.dispatchCustomShortcut(&fyne.ShortcutCopy{}) {
		t.Fatal("non-CustomShortcut must not match")
	}
}

func TestPasteAwareEntry_EscapeForwardsToEscStack(t *testing.T) {
	e := newPasteAwareEntry(nil, nil)
	called := 0
	pop := pushEsc(func() { called++ })
	t.Cleanup(pop) // cleanup if test fails before handleEsc consumes the entry

	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})

	if called != 1 {
		t.Fatalf("Escape should pop + invoke the top dismiss exactly once, got %d", called)
	}
}

func TestPasteAwareEntry_AddCustomShortcutNilArgsAreSafe(t *testing.T) {
	e := newPasteAwareEntry(nil, nil)
	// Both the nil-shortcut and nil-handler paths must be silent no-ops
	// (no panic, no map insertion).
	e.AddCustomShortcut(nil, func() {})
	e.AddCustomShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyJ, Modifier: fyne.KeyModifierControl}, nil)
	if e.dispatchCustomShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyJ, Modifier: fyne.KeyModifierControl}) {
		t.Fatal("nil-handler registration must not be retrievable")
	}
}
