package ui

import (
	"os/exec"
	"strings"
	"sync"
	"time"
)

// notifySendAvailable is checked once at startup; if missing we silently
// no-op so the app still works in environments without libnotify.
var (
	notifyOnce      sync.Once
	notifySendPath  string
)

// hasNotifySend returns true if the OS has notify-send available.
func hasNotifySend() bool {
	notifyOnce.Do(func() {
		if path, err := exec.LookPath("notify-send"); err == nil {
			notifySendPath = path
		}
	})
	return notifySendPath != ""
}

// rateLimit dedupes bursts: at most one notification per (sender, body)
// per coalesceWindow. Prevents a flood when HistorySync redelivers messages
// or the user is in a very chatty group.
var (
	notifyMu       sync.Mutex
	lastNotifyKey  string
	lastNotifyAt   time.Time
	coalesceWindow = 800 * time.Millisecond
)

// Notify shows a desktop notification using notify-send. Drops on platforms
// without libnotify. summary is the title (typically sender or chat name);
// body is the message preview. Both are passed verbatim — caller is
// responsible for any truncation.
func Notify(summary, body string) {
	if !hasNotifySend() {
		return
	}
	if summary == "" {
		summary = "WhatsApp Alt"
	}
	body = strings.ReplaceAll(body, "\x00", "")

	notifyMu.Lock()
	key := summary + "\x00" + body
	now := time.Now()
	if key == lastNotifyKey && now.Sub(lastNotifyAt) < coalesceWindow {
		notifyMu.Unlock()
		return
	}
	lastNotifyKey = key
	lastNotifyAt = now
	notifyMu.Unlock()

	go func() {
		// -a sets the app name (visible in some notification daemons).
		// -t expires after 5s; -u normal urgency.
		cmd := exec.Command(notifySendPath,
			"-a", "WhatsApp Alt",
			"-u", "normal",
			"-t", "5000",
			summary,
			body,
		)
		if err := cmd.Start(); err == nil {
			cmd.Wait()
		}
	}()
}

// NotifyMessage is a convenience wrapper for chat-message notifications.
// title becomes "<chatName>" if chat is a group (so user sees which group),
// or just senderName for DMs. body is the preview text.
func NotifyMessage(senderName, chatName, preview string, isGroup bool) {
	if isGroup {
		title := chatName
		if title == "" {
			title = senderName
		}
		body := preview
		if senderName != "" && senderName != chatName {
			body = senderName + ": " + preview
		}
		Notify(title, truncate(body, 200))
		return
	}
	Notify(senderName, truncate(preview, 200))
}
