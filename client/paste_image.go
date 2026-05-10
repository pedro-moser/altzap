package client

import (
	"errors"
	"os"
	"os/exec"
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

// detectClipboardImageReader picks the right command-line tool for the
// current display server. Wayland gets wl-paste; everything else
// (assumed X11) gets xclip. Results in a closure so callers don't have
// to spawn a subprocess until they actually want to paste.
func detectClipboardImageReader() func() ([]byte, error) {
	useWayland := os.Getenv("WAYLAND_DISPLAY") != ""
	return func() ([]byte, error) {
		var cmd *exec.Cmd
		if useWayland {
			cmd = exec.Command("wl-paste", "--type", "image/png", "--no-newline")
		} else {
			cmd = exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
		}
		out, err := cmd.Output()
		if err != nil {
			return nil, err
		}
		return out, nil
	}
}
