package client

import (
	"errors"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// uriListMIME is what file managers (Dolphin, Nautilus, Thunar…) offer when
// files are copied: one file:// URI per line, CRLF-separated per RFC 2483.
// Nautilus additionally offers x-special/gnome-copied-files, whose payload
// is the same URI list prefixed by a "copy"/"cut" header line.
const (
	uriListMIME    = "text/uri-list"
	gnomeFilesMIME = "x-special/gnome-copied-files"
)

// PasteFilesFromClipboard returns the local paths of files currently copied
// to the clipboard (e.g. via Ctrl+C in a file manager), or an error when
// the clipboard holds no file list. Paths are validated (regular files
// only) and belong to the user — callers must never delete them.
func PasteFilesFromClipboard() ([]string, error) {
	return pasteFilesWith(clipboardMIMETypes, readClipboardType)
}

// pasteFilesWith is the testable seam: listTypes enumerates the offered
// MIME types and readType fetches one of them.
func pasteFilesWith(listTypes func() ([]string, error), readType func(string) ([]byte, error)) ([]string, error) {
	types, err := listTypes()
	if err != nil {
		return nil, err
	}

	mime := ""
	for _, t := range types {
		switch strings.TrimSpace(t) {
		case uriListMIME:
			mime = uriListMIME
		case gnomeFilesMIME:
			if mime == "" {
				mime = gnomeFilesMIME
			}
		}
	}
	if mime == "" {
		return nil, errors.New("clipboard holds no file list")
	}

	data, err := readType(mime)
	if err != nil {
		return nil, err
	}

	paths := existingFiles(parseURIList(data, mime == gnomeFilesMIME))
	if len(paths) == 0 {
		return nil, errors.New("clipboard file list has no usable local files")
	}
	return paths, nil
}

// parseURIList extracts local paths from a text/uri-list payload: one URI
// per line (CRLF or LF), comments start with '#', URIs are percent-encoded.
// gnomeFormat additionally skips the leading "copy"/"cut" verb line of
// x-special/gnome-copied-files. Non-file schemes are ignored. Pure helper.
func parseURIList(data []byte, gnomeFormat bool) []string {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if gnomeFormat && i == 0 && (line == "copy" || line == "cut") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || u.Scheme != "file" || u.Path == "" {
			continue
		}
		out = append(out, u.Path) // url.Parse already percent-decodes Path
	}
	return out
}

// existingFiles keeps only paths that point at regular files — clipboard
// lists can reference directories or files deleted since the copy.
func existingFiles(paths []string) []string {
	out := paths[:0]
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			out = append(out, p)
		}
	}
	return out
}

// clipboardMIMETypes lists the MIME types the current clipboard offers.
// Wayland: wl-paste --list-types; X11: xclip TARGETS.
func clipboardMIMETypes() ([]string, error) {
	var cmd *exec.Cmd
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		cmd = exec.Command("wl-paste", "--list-types")
	} else {
		cmd = exec.Command("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n"), nil
}

// readClipboardType fetches the clipboard payload for one MIME type.
func readClipboardType(mime string) ([]byte, error) {
	var cmd *exec.Cmd
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		cmd = exec.Command("wl-paste", "--type", mime, "--no-newline")
	} else {
		cmd = exec.Command("xclip", "-selection", "clipboard", "-t", mime, "-o")
	}
	return cmd.Output()
}
