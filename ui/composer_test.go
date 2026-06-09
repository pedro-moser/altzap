package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
)

// TestComposerEnterSends covers the multi-line composer's keyboard send
// path: plain Enter fires OnSendRequested, Shift+Enter falls through to
// the embedded Entry (newline). The widget must be rendered in a test
// window first — Entry's selection state only exists post-renderer.
func TestComposerEnterSends(t *testing.T) {
	test.NewApp()
	e := newPasteAwareEntry(func() (string, error) { return "", nil }, nil)
	w := test.NewWindow(e)
	defer w.Close()
	sent := 0
	e.OnSendRequested = func() { sent++ }

	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if sent != 1 {
		t.Fatalf("plain Enter should send, got %d sends", sent)
	}
	if e.Text != "" {
		t.Fatalf("plain Enter must not insert a newline, text = %q", e.Text)
	}

	e.KeyDown(&fyne.KeyEvent{Name: desktop.KeyShiftLeft})
	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if sent != 1 {
		t.Fatalf("Shift+Enter must not send, got %d sends", sent)
	}
	if e.Text != "\n" {
		t.Fatalf("Shift+Enter should insert a newline, text = %q", e.Text)
	}

	e.KeyUp(&fyne.KeyEvent{Name: desktop.KeyShiftLeft})
	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEnter})
	if sent != 2 {
		t.Fatalf("Enter after releasing Shift should send again, got %d", sent)
	}
}

func TestComposerIsMultiLine(t *testing.T) {
	e := newPasteAwareEntry(func() (string, error) { return "", nil }, nil)
	if !e.MultiLine {
		t.Fatal("composer entry must be multi-line for Shift+Enter line breaks")
	}
}
