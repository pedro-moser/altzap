package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// commonEmojis is the curated set shown in the picker grid. Ordered
// roughly by frequency on Brazilian Portuguese chats — faces first,
// then hands, hearts, and a handful of useful symbols. 36 entries fit
// a 6×6 grid without scrolling.
var commonEmojis = []string{
	"😂", "❤️", "👍", "🙏", "🥰", "😊",
	"😍", "😘", "🤣", "😅", "😮", "😢",
	"😎", "🤔", "🥺", "😏", "🙄", "😴",
	"👏", "💪", "🤝", "👌", "✋", "🫶",
	"🔥", "✨", "💯", "🎉", "🥳", "🎂",
	"✅", "❌", "⭐", "💡", "📌", "👀",
}

// showEmojiPickerNear pops a 6-column grid of emojis above an anchor
// widget. Click an emoji → onPick(emoji) + popup closes. ESC also closes
// (registered on the global stack). Used both for inserting into the
// message input and for adding reactions.
func showEmojiPickerNear(c fyne.Canvas, anchor fyne.CanvasObject, onPick func(string)) {
	if c == nil || anchor == nil {
		return
	}

	var popup *widget.PopUp
	var popEsc func()

	dismiss := func() {
		if popEsc != nil {
			popEsc()
			popEsc = nil
		}
		if popup != nil {
			popup.Hide()
		}
	}

	grid := container.New(layout.NewGridLayout(6))
	for _, e := range commonEmojis {
		emoji := e // capture per-iteration value
		btn := widget.NewButton(emoji, func() {
			onPick(emoji)
			dismiss()
		})
		btn.Importance = widget.LowImportance
		grid.Add(btn)
	}

	popup = widget.NewPopUp(container.NewPadded(grid), c)
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(anchor)
	// Pop above the anchor so it floats out of the input strip / bubble
	// rather than into it. 4px breathing room.
	popup.ShowAtPosition(fyne.NewPos(pos.X, pos.Y-popup.MinSize().Height-4))

	popEsc = pushEsc(dismiss)
}

// showEmojiPicker is the input-bar variant: inserts the chosen emoji
// at the caret in the message input.
func (cv *ChatView) showEmojiPicker(anchor fyne.CanvasObject) {
	if cv.messageInput == nil {
		return
	}
	showEmojiPickerNear(cv.window.Canvas(), anchor, cv.insertEmoji)
}

// showReactionPickerFor is the bubble variant: sends the chosen emoji
// as a reaction targeting msg.
func (cv *ChatView) showReactionPickerFor(msg *Message, anchor fyne.CanvasObject) {
	if msg == nil {
		return
	}
	showEmojiPickerNear(cv.window.Canvas(), anchor, func(emoji string) {
		cv.sendReaction(msg, emoji)
	})
}

// insertEmoji writes s into messageInput at the caret. Single-line entry,
// so CursorColumn is the byte offset; for empty/zero-cursor cases SetText
// + cursor restoration suffices.
func (cv *ChatView) insertEmoji(s string) {
	if cv.messageInput == nil {
		return
	}
	text := cv.messageInput.Text
	pos := cv.messageInput.CursorColumn
	if pos < 0 {
		pos = 0
	}
	if pos > len(text) {
		pos = len(text)
	}
	newText := text[:pos] + s + text[pos:]
	cv.messageInput.SetText(newText)
	cv.messageInput.CursorColumn = pos + len(s)
	cv.window.Canvas().Focus(cv.messageInput)
}
