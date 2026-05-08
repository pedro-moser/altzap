package ui

import (
	"fmt"
	"image/color"
	"sort"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"altzap/client"
)

// buildReplyBox renders a small framed area shown above a bubble's content
// when this message is a reply. Mirrors WhatsApp's "quoted preview" — colored
// left bar, sender name in the bar's color, then a one-line preview.
func buildReplyBox(msg *Message) fyne.CanvasObject {
	if msg.ReplyToID == "" {
		return nil
	}

	senderName := msg.ReplyToSenderName
	if senderName == "" {
		senderName = "Someone"
	}

	preview := msg.ReplyToText
	if preview == "" {
		switch msg.ReplyToMediaType {
		case "image":
			preview = "📷 Photo"
		case "video":
			preview = "🎬 Video"
		case "audio":
			preview = "🎵 Audio"
		case "voice":
			preview = "🎤 Voice message"
		case "document":
			preview = "📎 Document"
		case "sticker":
			preview = "🎨 Sticker"
		default:
			preview = "(message)"
		}
	}
	preview = truncate(preview, 80)

	accent := avatarColor(senderName)

	leftBar := canvas.NewRectangle(accent)
	leftBar.SetMinSize(fyne.NewSize(3, 0))

	senderText := canvas.NewText(senderName, accent)
	senderText.TextStyle.Bold = true
	senderText.TextSize = 12

	previewLbl := widget.NewLabel(preview)
	previewLbl.Importance = widget.LowImportance
	previewLbl.Truncation = fyne.TextTruncateEllipsis

	textCol := container.NewVBox(senderText, previewLbl)

	bgC := color.RGBA{R: 0x31, G: 0x32, B: 0x44, A: 0x40}
	bg := canvas.NewRectangle(bgC)
	bg.CornerRadius = 4

	row := container.NewBorder(nil, nil, leftBar, nil, container.NewPadded(textCol))
	return container.NewStack(bg, row)
}

// buildReactionsRow renders a horizontal list of emoji chips with counts,
// grouped by emoji. Returns nil when there are no reactions.
func buildReactionsRow(msg *Message) fyne.CanvasObject {
	if len(msg.Reactions) == 0 {
		return nil
	}

	counts := map[string]int{}
	order := []string{}
	for _, r := range msg.Reactions {
		if _, ok := counts[r.Emoji]; !ok {
			order = append(order, r.Emoji)
		}
		counts[r.Emoji]++
	}
	sort.SliceStable(order, func(i, j int) bool {
		// Larger counts first; alphabetic tiebreak.
		if counts[order[i]] != counts[order[j]] {
			return counts[order[i]] > counts[order[j]]
		}
		return order[i] < order[j]
	})

	chips := make([]fyne.CanvasObject, 0, len(order))
	for _, emoji := range order {
		chip := buildReactionChip(emoji, counts[emoji])
		chips = append(chips, chip)
	}
	return container.NewHBox(chips...)
}

func buildReactionChip(emoji string, count int) fyne.CanvasObject {
	bg := canvas.NewRectangle(color.RGBA{R: 0x45, G: 0x47, B: 0x5a, A: 0xc0})
	bg.CornerRadius = 10

	label := emoji
	if count > 1 {
		label = fmt.Sprintf("%s %d", emoji, count)
	}
	txt := canvas.NewText(label, ctpText)
	txt.TextSize = 13
	txt.TextStyle.Bold = false

	padded := container.NewPadded(container.NewCenter(txt))
	return container.NewStack(bg, padded)
}

// applyReactionUpdate mutates an in-memory Message's Reactions to reflect
// the most recent server-side state. Idempotent.
func applyReactionUpdate(m *Message, update []client.SavedReaction) {
	m.Reactions = append(m.Reactions[:0], update...)
}
