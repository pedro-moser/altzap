package ui

import (
	"errors"
	"os/exec"
	"sync"
	"syscall"

	"fyne.io/fyne/v2"
)

// audio_player.go runs at most one ffplay child at a time. Bubbles call
// playAudio with their msg ID + path + state callback; the controller
// kills any previous playback before starting the new one and fires the
// callback (on the UI thread) on natural exit so the button can revert.

var (
	audioMu        sync.Mutex
	audioCmd       *exec.Cmd
	audioActiveID  string
	audioCallbacks = map[string]func(playing bool){}
)

// hasFFplay checks at startup whether ffplay is installed. We don't want to
// fail at click time; the audio bubble disables its play button when missing.
var hasFFplay = func() bool {
	_, err := exec.LookPath("ffplay")
	return err == nil
}()

// isPlayingMsg reports whether the given message ID is the one currently
// playing. Used to render the right initial icon when a bubble is rebuilt.
func isPlayingMsg(msgID string) bool {
	audioMu.Lock()
	defer audioMu.Unlock()
	return audioCmd != nil && audioActiveID == msgID
}

// playAudio starts ffplay for path. onState fires with `true` on a
// successful start and `false` when playback finishes (natural EOF, error,
// or user-initiated stop). Calling playAudio while another track is
// playing stops the prior one first and fires its onState(false).
func playAudio(msgID, path string, onState func(playing bool)) error {
	if !hasFFplay {
		return errors.New("ffplay not in PATH — install ffmpeg/ffplay for inline audio")
	}
	if path == "" {
		return errors.New("no media path")
	}

	stopAudio() // tear down any previous playback

	cmd := exec.Command("ffplay",
		"-nodisp",     // no video window
		"-autoexit",   // exit when audio ends
		"-loglevel", "error",
		path,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}

	audioMu.Lock()
	audioCmd = cmd
	audioActiveID = msgID
	audioCallbacks[msgID] = onState
	audioMu.Unlock()

	if onState != nil {
		onState(true)
	}

	go func() {
		_ = cmd.Wait()

		audioMu.Lock()
		if audioCmd != cmd {
			// Another playback already replaced us — its own callback handled state.
			audioMu.Unlock()
			return
		}
		cb := audioCallbacks[msgID]
		audioCmd = nil
		audioActiveID = ""
		delete(audioCallbacks, msgID)
		audioMu.Unlock()

		if cb != nil {
			fyne.Do(func() { cb(false) })
		}
	}()

	return nil
}

// StopAudioIfActive is the public lifecycle hook — wired from main.go's
// OnStopped so we don't leave an ffplay child running after the app exits.
func StopAudioIfActive() {
	stopAudio()
}

// stopAudio terminates the running playback (if any) and fires the active
// callback with `false`. Safe to call when nothing is playing.
func stopAudio() {
	audioMu.Lock()
	cmd := audioCmd
	id := audioActiveID
	cb := audioCallbacks[id]
	if cmd != nil {
		audioCmd = nil
		audioActiveID = ""
		delete(audioCallbacks, id)
	}
	audioMu.Unlock()

	if cmd == nil {
		return
	}
	if cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	go func() {
		_ = cmd.Wait()
		if cb != nil {
			fyne.Do(func() { cb(false) })
		}
	}()
}
