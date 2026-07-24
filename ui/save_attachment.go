package ui

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"

	"altzap/client"
)

// mediaNamePrefix gives unnamed media a WhatsApp-style filename prefix.
// Only documents carry a sender-declared filename; everything else has to
// be named here.
func mediaNamePrefix(mediaType string) string {
	switch mediaType {
	case "image":
		return "IMG"
	case "video":
		return "VID"
	case "voice":
		return "PTT"
	case "audio":
		return "AUD"
	case "sticker":
		return "STK"
	}
	return "FILE"
}

// sanitizeSaveName collapses a sender-supplied filename to a bare basename.
//
// FileName arrives over the network, so a crafted "../../.bashrc" must never
// reach the save dialog as-is. Control characters are dropped too — they
// render as garbage in the name field and some are illegal on other
// filesystems. Returns "" when nothing usable survives.
func sanitizeSaveName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	// Normalize Windows separators first so a "..\..\x" is caught by the
	// same Clean+Base collapse as its POSIX twin.
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(filepath.Clean("/" + name))
	if name == "." || name == ".." || name == string(filepath.Separator) {
		return ""
	}
	return name
}

// saveExt picks the extension for the saved copy.
//
// The cached file's own extension is normally right — it was chosen from the
// mimetype at download time. The exception is ".bin", which is what
// client.ExtForMime returns for every type it doesn't know (.docx, .md,
// .xlsx…). Handing the user a ".bin" would strip the OS of any way to route
// the file, so that case re-derives from the mimetype and, failing that,
// returns "" and lets the user type an extension themselves.
func saveExt(msg *Message) string {
	if ext := filepath.Ext(msg.MediaPath); ext != "" && !strings.EqualFold(ext, ".bin") {
		return ext
	}
	if ext := client.ExtForMime(msg.Mimetype); ext != ".bin" {
		return ext
	}
	return ""
}

// suggestedSaveName is the filename the save dialog opens with: the sender's
// own name for documents, an "IMG-20260724-085936.jpg"-style stamp for
// everything else.
func suggestedSaveName(msg *Message) string {
	if msg == nil {
		return ""
	}
	if base := sanitizeSaveName(msg.FileName); base != "" {
		if filepath.Ext(base) != "" {
			return base
		}
		return base + saveExt(msg)
	}
	stamp := msg.Timestamp.Format("20060102-150405")
	return mediaNamePrefix(msg.MediaType) + "-" + stamp + saveExt(msg)
}

// downloadsLister resolves the user's download directory so the save dialog
// opens where a browser would rather than in the process CWD. Returns nil
// when it can't be resolved — the dialog then keeps Fyne's own default.
func downloadsLister() fyne.ListableURI {
	dir := os.Getenv("XDG_DOWNLOAD_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		dir = filepath.Join(home, "Downloads")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil
	}
	lister, err := storage.ListerForURI(storage.NewFileURI(dir))
	if err != nil {
		return nil
	}
	return lister
}

// saveAttachment opens a "Save as" dialog for a media message and copies the
// cached file to wherever the user picks. The cached original stays put:
// forwarding and replaying the message still read from it.
func (cv *ChatView) saveAttachment(msg *Message) {
	if msg == nil || cv.window == nil {
		return
	}
	src := client.AbsoluteMediaPath(msg.MediaPath)
	if src == "" {
		dialog.ShowInformation("Not downloaded yet",
			"This attachment hasn't finished downloading.", cv.window)
		return
	}
	if _, err := os.Stat(src); err != nil {
		dialog.ShowError(fmt.Errorf("attachment file is unavailable: %w", err), cv.window)
		return
	}

	save := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil {
			dialog.ShowError(err, cv.window)
			return
		}
		if w == nil {
			return // cancelled
		}
		go cv.copyAttachmentTo(src, w)
	}, cv.window)
	save.SetFileName(suggestedSaveName(msg))
	if dir := downloadsLister(); dir != nil {
		save.SetLocation(dir)
	}
	save.Resize(fyne.NewSize(700, 500))
	save.Show()
}

// copyAttachmentTo streams the cached file into the chosen destination off
// the UI thread — a 50 MB video must not freeze the chat while it copies.
// Success is reported as a desktop notification rather than a modal so the
// user isn't made to dismiss a dialog after every save.
func (cv *ChatView) copyAttachmentTo(src string, dst fyne.URIWriteCloser) {
	name := dst.URI().Name()
	err := func() (err error) {
		defer func() {
			// Close reports the real error for buffered/remote writers;
			// don't let a successful io.Copy mask a failed flush.
			if cerr := dst.Close(); err == nil {
				err = cerr
			}
		}()
		in, oerr := os.Open(src)
		if oerr != nil {
			return oerr
		}
		defer in.Close()
		_, err = io.Copy(dst, in)
		return err
	}()

	if err != nil {
		log.Printf("save attachment %s -> %s: %v", src, dst.URI().Path(), err)
		fyne.Do(func() {
			dialog.ShowError(fmt.Errorf("could not save %s: %w", name, err), cv.window)
		})
		return
	}
	Notify("AltZap", "Saved "+name)
}
