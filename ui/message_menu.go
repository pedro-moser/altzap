package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// wrapBubbleGestures gives a bubble's content the shared mouse gestures:
// middle-click copies the text/caption, double-click starts a reply and
// right-click opens the message context menu. Inner interactive widgets
// (Open/Play buttons, hyperlinks, the reactions "+") still win on their own
// pixels — Fyne's hit-test dispatches to the deepest matching object — so
// wrapping a media bubble's whole body is safe.
func (cv *ChatView) wrapBubbleGestures(msg *Message, content fyne.CanvasObject) fyne.CanvasObject {
	if cv.window == nil || msg.Deleted {
		return content
	}
	target := msg
	var onCopy func()
	if msg.Text != "" {
		textCopy := msg.Text
		onCopy = func() {
			if c := cv.window.Clipboard(); c != nil {
				c.SetContent(textCopy)
			}
		}
	}
	return newClickableBubble(content,
		onCopy,
		func() { cv.beginReply(target) },
		func(ev *fyne.PointEvent) {
			if ev != nil {
				cv.showMessageContextMenu(target, ev.AbsolutePosition)
			}
		},
	)
}

// showMessageContextMenu pops the per-bubble right-click menu at pos
// (canvas coordinates, as delivered by TappedSecondary — PopUpMenu clamps
// to the canvas edges itself). No pushEsc here: PopUpMenu takes keyboard
// focus and handles ESC/outside-tap dismissal on its own, same contract as
// showChatMenu and showAttachMenu.
func (cv *ChatView) showMessageContextMenu(msg *Message, pos fyne.Position) {
	if msg == nil || cv.window == nil {
		return
	}
	c := cv.window.Canvas()

	items := make([]*fyne.MenuItem, 0, 4)
	if msg.Text != "" {
		items = append(items, fyne.NewMenuItem("Copy", func() {
			if cb := cv.window.Clipboard(); cb != nil {
				cb.SetContent(msg.Text)
			}
		}))
	}
	items = append(items,
		fyne.NewMenuItem("Reply", func() { cv.beginReply(msg) }),
		fyne.NewMenuItem("React…", func() {
			showEmojiPickerAt(c, pos, func(emoji string) { cv.sendReaction(msg, emoji) })
		}),
	)

	forward := fyne.NewMenuItem("Forward…", func() { cv.showForwardPicker(msg) })
	if msg.MediaType != "" && msg.MediaPath == "" {
		// Media still downloading (or never downloaded — history-sync
		// records): nothing local to re-upload yet.
		forward.Disabled = true
	}
	items = append(items, fyne.NewMenuItemSeparator(), forward)

	if msg.IsOwn {
		items = append(items, fyne.NewMenuItemSeparator())
		if msg.MediaType == "" && msg.Text != "" {
			edit := fyne.NewMenuItem("Edit…", func() { cv.showEditDialog(msg) })
			edit.Disabled = !canEditMessage(msg, time.Now())
			items = append(items, edit)
		}
		del := fyne.NewMenuItem("Delete for everyone", func() { cv.confirmDeleteForEveryone(msg) })
		// A pending ID may not exist server-side yet.
		del.Disabled = msg.Status == "pending"
		items = append(items, del)
	}

	widget.NewPopUpMenu(fyne.NewMenu("", items...), c).ShowAtPosition(pos)
}
