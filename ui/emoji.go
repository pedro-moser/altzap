package ui

import (
	"strings"

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

// insertAtRuneOffset inserts ins into s at rune index pos (clamped to the
// string's bounds) and returns the new string plus the rune index just past
// the insertion. Pure helper so the offset math is unit-testable.
func insertAtRuneOffset(s, ins string, pos int) (string, int) {
	runes := []rune(s)
	if pos < 0 {
		pos = 0
	}
	if pos > len(runes) {
		pos = len(runes)
	}
	out := string(runes[:pos]) + ins + string(runes[pos:])
	return out, pos + len([]rune(ins))
}

// runeOffsetForCursor converts a multi-line Entry caret (row, col — both
// rune-indexed within their line) into a global rune offset in text.
func runeOffsetForCursor(text string, row, col int) int {
	lines := strings.Split(text, "\n")
	if row < 0 {
		row = 0
	}
	if row >= len(lines) {
		row = len(lines) - 1
	}
	offset := 0
	for i := 0; i < row; i++ {
		offset += len([]rune(lines[i])) + 1 // +1 for the newline itself
	}
	lineLen := len([]rune(lines[row]))
	if col < 0 {
		col = 0
	}
	if col > lineLen {
		col = lineLen
	}
	return offset + col
}

// cursorForRuneOffset is the inverse: global rune offset → (row, col).
func cursorForRuneOffset(text string, off int) (row, col int) {
	runes := []rune(text)
	if off < 0 {
		off = 0
	}
	if off > len(runes) {
		off = len(runes)
	}
	for i := 0; i < off; i++ {
		if runes[i] == '\n' {
			row++
			col = 0
		} else {
			col++
		}
	}
	return row, col
}

// insertEmoji writes s into messageInput at the caret. Entry exposes the
// caret as (CursorRow, CursorColumn) rune indices — never byte offsets —
// so all slicing goes through the rune-based helpers above.
func (cv *ChatView) insertEmoji(s string) {
	if cv.messageInput == nil {
		return
	}
	text := cv.messageInput.Text
	pos := runeOffsetForCursor(text, cv.messageInput.CursorRow, cv.messageInput.CursorColumn)
	newText, newPos := insertAtRuneOffset(text, s, pos)
	cv.messageInput.SetText(newText)
	cv.messageInput.CursorRow, cv.messageInput.CursorColumn = cursorForRuneOffset(newText, newPos)
	cv.window.Canvas().Focus(cv.messageInput)
}
