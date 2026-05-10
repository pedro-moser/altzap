package client

import (
	"errors"
	"os"
	"testing"
)

func TestPasteImageFromBytes_RejectsEmpty(t *testing.T) {
	_, err := pasteImageFromBytes(nil)
	if err == nil {
		t.Fatal("expected error on empty clipboard, got nil")
	}
}

func TestPasteImageFromBytes_PersistsContent(t *testing.T) {
	// Tiny valid-looking PNG header — content is opaque to the helper, it
	// just round-trips the bytes to a tmp file.
	data := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 'x', 'y', 'z'}

	path, err := pasteImageFromBytes(data)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { os.Remove(path) })

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("data round-tripped wrong: want %x, got %x", data, got)
	}
}

func TestPasteImageFromBytes_PNGFilenameSuffix(t *testing.T) {
	// Important so confirmAndSendImage / SendImage downstream can mime-
	// detect from extension and Fyne's canvas.Image picks the right
	// decoder.
	data := []byte("dummy")
	path, err := pasteImageFromBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(path) })

	if got := path[len(path)-4:]; got != ".png" {
		t.Fatalf("path should end in .png, got %q", path)
	}
}

func TestPasteImageFromClipboard_ErrorBubblesUp(t *testing.T) {
	stub := func() ([]byte, error) {
		return nil, errors.New("no clipboard tool available")
	}
	if _, err := pasteImageFromClipboardWith(stub); err == nil {
		t.Fatal("expected error to bubble up")
	}
}

func TestPasteImageFromClipboard_EmptyOutputIsError(t *testing.T) {
	stub := func() ([]byte, error) { return nil, nil }
	if _, err := pasteImageFromClipboardWith(stub); err == nil {
		t.Fatal("empty clipboard should return an error, not an empty path")
	}
}

func TestPasteImageFromClipboard_HappyPathSavesFile(t *testing.T) {
	data := []byte{0x89, 0x50, 0x4e, 0x47}
	stub := func() ([]byte, error) { return data, nil }

	path, err := pasteImageFromClipboardWith(stub)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(path) })

	got, _ := os.ReadFile(path)
	if string(got) != string(data) {
		t.Fatalf("data not persisted: want %x, got %x", data, got)
	}
}
