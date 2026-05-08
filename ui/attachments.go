package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"go.mau.fi/whatsmeow/types"
)

// recorder owns a single ffmpeg-based voice recording in flight.
type recorder struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	path    string
	started time.Time
}

// active reports whether a recording is in progress.
func (r *recorder) active() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cmd != nil
}

// start spawns ffmpeg recording from the default Pulse/Pipewire input into an
// OGG/Opus file. Returns the output path or an error.
func (r *recorder) start() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil {
		return "", errors.New("already recording")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", fmt.Errorf("ffmpeg not found in PATH — install it for voice recording")
	}

	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("wa_voice_%d.ogg", time.Now().Unix()))
	cmd := exec.Command(
		"ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "pulse", "-i", "default",
		"-c:a", "libopus", "-b:a", "32k",
		tmpFile,
	)
	// Own process group so we can deliver SIGINT to the ffmpeg child only.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start ffmpeg: %w", err)
	}
	r.cmd = cmd
	r.path = tmpFile
	r.started = time.Now()
	return tmpFile, nil
}

// stop sends SIGINT so ffmpeg flushes the trailer, then waits for it. Returns
// the recorded file path on success.
func (r *recorder) stop() (string, error) {
	r.mu.Lock()
	cmd := r.cmd
	path := r.path
	r.cmd = nil
	r.path = ""
	r.mu.Unlock()

	if cmd == nil {
		return "", errors.New("not recording")
	}
	if cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGINT)
	}
	// ffmpeg exits with code 255 after SIGINT — that's fine, the file is valid.
	_ = cmd.Wait()

	if info, err := os.Stat(path); err != nil || info.Size() < 200 {
		return "", fmt.Errorf("recording too short or empty")
	}
	return path, nil
}

// cancel kills any in-flight recording without sending the file.
func (r *recorder) cancel() {
	r.mu.Lock()
	cmd := r.cmd
	path := r.path
	r.cmd = nil
	r.path = ""
	r.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	if path != "" {
		_ = os.Remove(path)
	}
}

// elapsed reports how long the current recording has been running.
func (r *recorder) elapsed() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd == nil {
		return 0
	}
	return time.Since(r.started)
}

// showAttachMenu pops an attachment-type chooser anchored over the paperclip
// button, then triggers the appropriate file picker.
func (cv *ChatView) showAttachMenu(anchor fyne.CanvasObject) {
	canvas := cv.window.Canvas()

	imgItem := fyne.NewMenuItem("Image", func() { cv.pickAndSend(attachImage) })
	imgItem.Icon = theme.FileImageIcon()

	audioItem := fyne.NewMenuItem("Audio file", func() { cv.pickAndSend(attachAudio) })
	audioItem.Icon = theme.FileAudioIcon()

	docItem := fyne.NewMenuItem("Document", func() { cv.pickAndSend(attachDocument) })
	docItem.Icon = theme.FileIcon()

	menu := fyne.NewMenu("Attach", imgItem, audioItem, docItem)
	popup := widget.NewPopUpMenu(menu, canvas)

	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(anchor)
	popup.ShowAtPosition(fyne.NewPos(pos.X, pos.Y-popup.MinSize().Height-4))
}

type attachKind int

const (
	attachImage attachKind = iota
	attachAudio
	attachDocument
)

func (cv *ChatView) pickAndSend(kind attachKind) {
	if cv.currentChatJID == "" {
		dialog.ShowInformation("No chat", "Open a chat first.", cv.window)
		return
	}
	jid, err := types.ParseJID(cv.currentChatJID)
	if err != nil {
		dialog.ShowError(err, cv.window)
		return
	}

	picker := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, cv.window)
			return
		}
		if reader == nil {
			return // cancelled
		}
		path := reader.URI().Path()
		_ = reader.Close()
		if path == "" {
			dialog.ShowError(errors.New("file has no local path"), cv.window)
			return
		}

		go func() {
			var sendErr error
			switch kind {
			case attachImage:
				sendErr = cv.waClient.SendImage(jid, path, "")
			case attachAudio:
				sendErr = cv.waClient.SendAudio(jid, path)
			case attachDocument:
				sendErr = cv.waClient.SendFile(jid, path, filepath.Base(path))
			}
			if sendErr != nil {
				fyne.Do(func() { dialog.ShowError(sendErr, cv.window) })
				return
			}
			fyne.Do(func() {
				cv.appendMessageBubble(&Message{
					Sender:    "You",
					Text:      "[" + attachLabel(kind) + ": " + filepath.Base(path) + "]",
					Timestamp: time.Now(),
					IsOwn:     true,
				})
			})
		}()
	}, cv.window)

	switch kind {
	case attachImage:
		picker.SetFilter(storage.NewExtensionFileFilter([]string{".png", ".jpg", ".jpeg", ".gif", ".webp"}))
	case attachAudio:
		picker.SetFilter(storage.NewExtensionFileFilter([]string{".ogg", ".mp3", ".m4a", ".aac", ".opus", ".wav"}))
	}
	picker.Resize(fyne.NewSize(700, 500))
	picker.Show()
}

func attachLabel(kind attachKind) string {
	switch kind {
	case attachImage:
		return "Image"
	case attachAudio:
		return "Audio"
	case attachDocument:
		return "Document"
	}
	return "File"
}

// onMicClicked toggles voice recording. First click starts ffmpeg; second
// click stops it and sends the resulting OGG/Opus to the open chat.
func (cv *ChatView) onMicClicked() {
	if cv.currentChatJID == "" {
		dialog.ShowInformation("No chat", "Open a chat first.", cv.window)
		return
	}

	if !cv.recorder.active() {
		path, err := cv.recorder.start()
		if err != nil {
			dialog.ShowError(err, cv.window)
			return
		}
		_ = path
		cv.setMicRecording(true)
		// Cosmetic: subtitle ticker showing elapsed time.
		go cv.tickRecordingSubtitle()
		return
	}

	// Stop & send.
	jid, err := types.ParseJID(cv.currentChatJID)
	if err != nil {
		cv.recorder.cancel()
		cv.setMicRecording(false)
		dialog.ShowError(err, cv.window)
		return
	}

	path, err := cv.recorder.stop()
	cv.setMicRecording(false)
	if err != nil {
		dialog.ShowError(err, cv.window)
		return
	}

	go func() {
		if err := cv.waClient.SendAudio(jid, path); err != nil {
			fyne.Do(func() { dialog.ShowError(err, cv.window) })
			return
		}
		fyne.Do(func() {
			cv.appendMessageBubble(&Message{
				Sender:    "You",
				Text:      "[Voice message]",
				Timestamp: time.Now(),
				IsOwn:     true,
			})
		})
		_ = os.Remove(path)
	}()
}

func (cv *ChatView) setMicRecording(on bool) {
	if cv.micBtn == nil {
		return
	}
	fyne.Do(func() {
		if on {
			cv.micBtn.SetIcon(theme.MediaStopIcon())
			cv.micBtn.Importance = widget.DangerImportance
		} else {
			cv.micBtn.SetIcon(theme.MediaRecordIcon())
			cv.micBtn.Importance = widget.MediumImportance
			// Restore subtitle to its normal value.
			if cv.chatSubtitle != nil {
				cv.chatSubtitle.SetText(cv.lastSubtitle)
			}
		}
		cv.micBtn.Refresh()
	})
}

// tickRecordingSubtitle shows a "Recording 0:05…" subtitle while the mic
// button is in record mode.
func (cv *ChatView) tickRecordingSubtitle() {
	if cv.chatSubtitle != nil {
		cv.lastSubtitle = cv.chatSubtitle.Text
	}
	for cv.recorder.active() {
		d := cv.recorder.elapsed()
		secs := int(d.Seconds())
		text := fmt.Sprintf("● Recording %d:%02d  (tap stop to send)", secs/60, secs%60)
		if cv.chatSubtitle != nil {
			fyne.Do(func() { cv.chatSubtitle.SetText(text) })
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// hasFFmpeg returns true when ffmpeg is available — used to enable/disable the mic.
func hasFFmpeg() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// StopRecordingIfActive is a safety net for window close — make sure we don't
// leave an ffmpeg child running.
func (cv *ChatView) StopRecordingIfActive() {
	if cv != nil && cv.recorder.active() {
		cv.recorder.cancel()
	}
}

// inputBarBuild assembles the [attach] [entry] [mic] [send] row used as the
// chat input area. Kept here so the chat_view file stays focused on layout.
func (cv *ChatView) inputBarBuild() fyne.CanvasObject {
	cv.messageInput = widget.NewEntry()
	cv.messageInput.PlaceHolder = "Type a message"
	cv.messageInput.OnSubmitted = func(string) { cv.sendMessage() }

	cv.attachBtn = widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		cv.showAttachMenu(cv.attachBtn)
	})
	cv.attachBtn.Importance = widget.MediumImportance

	cv.micBtn = widget.NewButtonWithIcon("", theme.MediaRecordIcon(), cv.onMicClicked)
	cv.micBtn.Importance = widget.MediumImportance
	if !hasFFmpeg() {
		cv.micBtn.Disable()
	}

	sendBtn := widget.NewButtonWithIcon("", theme.MailSendIcon(), cv.sendMessage)
	sendBtn.Importance = widget.HighImportance

	rightBtns := container.NewHBox(cv.micBtn, sendBtn)
	return container.NewBorder(nil, nil, cv.attachBtn, rightBtns, cv.messageInput)
}
