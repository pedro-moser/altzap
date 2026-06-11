package ui

import (
	"fmt"
	"strings"
	"time"

	"altzap/client"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"go.mau.fi/whatsmeow/types"
)

// canEditMessage gates the context menu's "Edit…" item: only our own,
// non-deleted, plain-text messages, already ACKed (a pending ID may not
// exist server-side yet) and still inside WhatsApp's edit window. Pure
// helper, unit-tested.
func canEditMessage(msg *Message, now time.Time) bool {
	if msg == nil || !msg.IsOwn || msg.Deleted {
		return false
	}
	if msg.MediaType != "" || msg.Text == "" {
		// Caption edits need the original media proto rebuilt — v2.
		return false
	}
	if msg.Status == "pending" {
		return false
	}
	return now.Sub(msg.Timestamp) <= client.EditWindow
}

// showEditDialog opens a pre-filled multi-line editor for one of our own
// messages. On confirm the edit is sent in the background; the client's
// local echo (OnMessageEdit) updates the bubble — no optimistic patch here,
// so a failed send can't leave the UI lying.
func (cv *ChatView) showEditDialog(msg *Message) {
	if msg == nil || cv.window == nil || cv.currentChatJID == "" {
		return
	}
	chatJID := cv.currentChatJID // capture: the dialog outlives chat switches

	entry := widget.NewMultiLineEntry()
	entry.Wrapping = fyne.TextWrapWord
	entry.SetText(msg.Text)

	d := dialog.NewCustomConfirm("Edit message", "Save", "Cancel",
		container.NewPadded(entry),
		func(ok bool) {
			newText := strings.TrimSpace(entry.Text)
			if !ok || newText == "" || newText == msg.Text {
				return
			}
			chat, err := types.ParseJID(chatJID)
			if err != nil {
				return
			}
			go func() {
				if err := cv.waClient.EditMessage(chat, msg.ID, newText); err != nil {
					fyne.Do(func() {
						dialog.ShowError(fmt.Errorf("edit failed: %w", err), cv.window)
					})
				}
			}()
		},
		cv.window,
	)
	d.Resize(fyne.NewSize(440, 220))
	d.Show()
	cv.window.Canvas().Focus(entry)
}

// confirmDeleteForEveryone asks before revoking one of our own messages —
// irreversible and visible to the whole chat. The bubble flips to the
// "deleted" placeholder via the client's local echo (OnMessageDelete).
func (cv *ChatView) confirmDeleteForEveryone(msg *Message) {
	if msg == nil || cv.window == nil || cv.currentChatJID == "" {
		return
	}
	chatJID := cv.currentChatJID // capture: the dialog outlives chat switches

	d := dialog.NewCustomConfirm("Delete message", "Delete for everyone", "Cancel",
		widget.NewLabel("This message will be deleted for everyone in this chat."),
		func(ok bool) {
			if !ok {
				return
			}
			chat, err := types.ParseJID(chatJID)
			if err != nil {
				return
			}
			go func() {
				if err := cv.waClient.RevokeMessage(chat, msg.ID); err != nil {
					fyne.Do(func() {
						dialog.ShowError(fmt.Errorf("delete failed: %w", err), cv.window)
					})
				}
			}()
		},
		cv.window,
	)
	d.Show()
}
