package client

import (
	"errors"
	"os"
	"strings"
)

// PasteImageFromClipboard reads an image from the system clipboard and
// writes it to a temporary file, returning the file's path. The caller
// is responsible for removing the file once consumed (e.g. after the
// upload completes).
//
// On Wayland it shells out to wl-paste; on X11 to xclip. Both tools are
// near-universal on Linux desktops; Pedro's daily driver is Hyprland
// which always provides wl-paste. Returns an error when no image
// content is on the clipboard or when neither tool is available.
func PasteImageFromClipboard() (string, error) {
	return pasteImageFromClipboardWith(detectClipboardImageReader())
}

// pasteImageFromClipboardWith is the testable seam: callers supply a
// function that fetches the clipboard payload as bytes (typically a
// subprocess wrapper). Splitting the byte-fetch from file persistence
// keeps the logic unit-testable without invoking a real wl-paste.
func pasteImageFromClipboardWith(read func() ([]byte, error)) (string, error) {
	data, err := read()
	if err != nil {
		return "", err
	}
	return pasteImageFromBytes(data)
}

// pasteImageFromBytes writes data to a temporary .png file and returns
// the path. Empty input returns an error rather than producing an empty
// file — callers want to discriminate "nothing pasted" from "saved".
func pasteImageFromBytes(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("clipboard contains no image")
	}
	f, err := os.CreateTemp("", "altzap-paste-*.png")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// detectClipboardImageReader returns a closure that fetches the clipboard's
// image payload, preferring image/png but falling back to whatever image/*
// type is actually offered (browsers and some apps offer only image/jpeg
// or image/webp). Listing types first also means "no image on clipboard"
// fails fast without a doomed payload fetch.
func detectClipboardImageReader() func() ([]byte, error) {
	return func() ([]byte, error) {
		types, err := clipboardMIMETypes()
		if err != nil {
			return nil, err
		}
		mime := pickImageMIME(types)
		if mime == "" {
			return nil, errors.New("clipboard contains no image")
		}
		return readClipboardType(mime)
	}
}

// pickImageMIME chooses which offered MIME type to fetch as the pasted
// image: image/png when available (lossless, the screenshot-tool default),
// otherwise the first image/* entry. Empty when none qualifies. Pure.
func pickImageMIME(types []string) string {
	first := ""
	for _, t := range types {
		t = strings.TrimSpace(t)
		if t == "image/png" {
			return t
		}
		if first == "" && strings.HasPrefix(t, "image/") {
			first = t
		}
	}
	return first
}
