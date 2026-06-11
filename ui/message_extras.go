package ui

import (
	"fmt"
	"image/color"
	"sort"

	"altzap/client"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"go.mau.fi/whatsmeow/types"
)

// isHumanName reports whether s reads like an actual display name rather
// than a bare phone number or LID hash — i.e., it contains at least one rune
// outside the digits-and-phone-punctuation alphabet.
func isHumanName(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r == '+', r == ' ', r == '-', r == '(', r == ')', r == '.':
		default:
			return true
		}
	}
	return false
}

// quotedSenderDisplay picks the name shown for a quote's author. stored is
// the name resolved (and frozen) at ingest time; jidStr the author's raw
// JID. A human-readable stored name wins; numeric leftovers — the LID hash
// that history sync froze before contact/LID mappings finished syncing, or
// a bare phone — are re-resolved through lookup at render time, which keeps
// improving as mappings land. Pure helper, unit-tested.
func quotedSenderDisplay(stored, jidStr string, lookup func(types.JID) string) string {
	if isHumanName(stored) {
		return stored
	}
	if jidStr == "" || lookup == nil {
		return stored
	}
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return stored
	}
	resolved := lookup(jid)
	if isHumanName(resolved) {
		return resolved
	}
	// Both numeric: a resolved phone number (LID→PN mapping landed) beats
	// the raw hash, but never downgrade an existing phone back to a hash.
	if resolved != "" && resolved != jid.User {
		return resolved
	}
	if stored != "" {
		return stored
	}
	if jid.Server != types.HiddenUserServer {
		return resolved // a bare phone number is still informative
	}
	return "" // a LID hash is meaningless noise — caller shows "Someone"
}

// buildReplyBox renders a small framed area shown above a bubble's content
// when this message is a reply. Mirrors WhatsApp's "quoted preview" — colored
// left bar, sender name in the bar's color, then a one-line preview.
// cv may be nil in tests; it's only used to re-resolve stale quoted names.
func buildReplyBox(msg *Message, cv *ChatView) fyne.CanvasObject {
	if msg.ReplyToID == "" {
		return nil
	}

	senderName := msg.ReplyToSenderName
	if cv != nil && cv.waClient != nil {
		senderName = quotedSenderDisplay(senderName, msg.ReplyToSenderJID, cv.waClient.LookupName)
	}
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

	bg := canvas.NewRectangle(color.RGBA{R: ctpSurface0.R, G: ctpSurface0.G, B: ctpSurface0.B, A: 0x40})
	bg.CornerRadius = 4

	row := container.NewBorder(nil, nil, leftBar, nil, container.NewPadded(textCol))
	return container.NewStack(bg, row)
}

// buildReactionsRow renders a horizontal list of existing reaction chips
// (grouped + count-sorted) followed by a small "+" button that opens the
// reaction picker. Always returns a non-nil row — there's always the add
// affordance even when no one has reacted yet.
func buildReactionsRow(msg *Message, cv *ChatView) fyne.CanvasObject {
	chips := make([]fyne.CanvasObject, 0, len(msg.Reactions)+1)

	if len(msg.Reactions) > 0 {
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
		for _, emoji := range order {
			chips = append(chips, buildReactionChip(emoji, counts[emoji]))
		}
	}

	// "+" button anchored after existing reactions. Created with nil
	// handler then patched so we have a stable ref to use as the popup
	// anchor (positioning needs to read its absolute coords).
	addBtn := widget.NewButtonWithIcon("", theme.ContentAddIcon(), nil)
	addBtn.Importance = widget.LowImportance
	addBtn.OnTapped = func() {
		if cv != nil {
			cv.showReactionPickerFor(msg, addBtn)
		}
	}
	chips = append(chips, addBtn)

	return container.NewHBox(chips...)
}

func buildReactionChip(emoji string, count int) fyne.CanvasObject {
	bg := canvas.NewRectangle(color.RGBA{R: ctpSurface1.R, G: ctpSurface1.G, B: ctpSurface1.B, A: 0xc0})
	bg.CornerRadius = 12

	label := emoji
	if count > 1 {
		label = fmt.Sprintf("%s %d", emoji, count)
	}
	txt := canvas.NewText(label, ctpText)
	txt.TextSize = 18
	txt.TextStyle.Bold = false

	padded := container.NewPadded(container.NewCenter(txt))
	return container.NewStack(bg, padded)
}

// applyReactionUpdate mutates an in-memory Message's Reactions to reflect
// the most recent server-side state. Idempotent.
func applyReactionUpdate(m *Message, update []client.SavedReaction) {
	m.Reactions = append(m.Reactions[:0], update...)
}
