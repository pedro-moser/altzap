package ui

import (
	"encoding/base64"
	"errors"
	"sort"
	"strings"

	"altzap/client"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"go.mau.fi/whatsmeow/types"
)

// forwardCandidate is one selectable destination in the forward picker.
type forwardCandidate struct {
	JID      string
	Name     string
	Subtitle string // "+<phone>", "Group" or ""
}

// mergeForwardCandidates builds the destination list: recent chats first
// (sidebar order — includes groups, which the contacts list never has),
// then address-book contacts not already covered, alphabetically. Twins are
// deduped both by JID and by canonical phone so a contact whose chat runs
// LID-addressed doesn't show up twice. Pure helper, unit-tested.
func mergeForwardCandidates(chats []client.Chat, contacts []client.Contact,
	phoneFor func(types.JID) string, nameFor func(types.JID) string) []forwardCandidate {

	out := make([]forwardCandidate, 0, len(chats)+len(contacts))
	seenJID := make(map[string]bool, len(chats))
	seenPhone := make(map[string]bool, len(chats))

	for _, c := range chats {
		jidStr := c.JID.String()
		if jidStr == "" || seenJID[jidStr] {
			continue
		}
		seenJID[jidStr] = true
		sub := ""
		if c.IsGroup {
			sub = "Group"
		} else if pn := phoneFor(c.JID); pn != "" {
			sub = "+" + pn
			seenPhone[pn] = true
		}
		name := c.DisplayName
		if name == "" {
			name = nameFor(c.JID)
		}
		out = append(out, forwardCandidate{JID: jidStr, Name: name, Subtitle: sub})
	}

	extra := make([]forwardCandidate, 0, len(contacts))
	for _, ct := range contacts {
		jidStr := ct.JID.String()
		if jidStr == "" || seenJID[jidStr] {
			continue
		}
		pn := phoneFor(ct.JID)
		if pn != "" && seenPhone[pn] {
			continue
		}
		seenJID[jidStr] = true
		if pn != "" {
			seenPhone[pn] = true
		}
		name := ct.Name
		if name == "" {
			name = nameFor(ct.JID)
		}
		sub := ""
		if pn != "" {
			sub = "+" + pn
		}
		extra = append(extra, forwardCandidate{JID: jidStr, Name: name, Subtitle: sub})
	}
	sort.SliceStable(extra, func(i, j int) bool {
		return strings.ToLower(extra[i].Name) < strings.ToLower(extra[j].Name)
	})

	return append(out, extra...)
}

// filterForwardCandidates returns the entries whose name, subtitle or JID
// contains the query (case-insensitive). Empty query returns all. Pure.
func filterForwardCandidates(all []forwardCandidate, query string) []forwardCandidate {
	ql := strings.ToLower(strings.TrimSpace(query))
	if ql == "" {
		return all
	}
	out := make([]forwardCandidate, 0, len(all))
	for _, c := range all {
		if strings.Contains(strings.ToLower(c.Name), ql) ||
			strings.Contains(strings.ToLower(c.Subtitle), ql) ||
			strings.Contains(strings.ToLower(c.JID), ql) {
			out = append(out, c)
		}
	}
	return out
}

// forwardSource snapshots the fields ForwardMessage needs from a UI message.
func forwardSource(msg *Message) client.SavedMessage {
	src := client.SavedMessage{
		ID:        msg.ID,
		Text:      msg.Text,
		MediaType: msg.MediaType,
		MediaPath: msg.MediaPath,
		Mimetype:  msg.Mimetype,
		FileName:  msg.FileName,
		FileSize:  msg.FileSize,
		Width:     msg.Width,
		Height:    msg.Height,
		Duration:  msg.Duration,
	}
	if len(msg.Thumb) > 0 {
		src.ThumbB64 = base64.StdEncoding.EncodeToString(msg.Thumb)
	}
	return src
}

// showForwardPicker opens the destination chooser for forwarding msg.
// Selecting an entry fires the forward in the background; the destination
// bubble lands via appendStoredMessage (same flow as media sends).
func (cv *ChatView) showForwardPicker(msg *Message) {
	if msg == nil || cv.window == nil {
		return
	}
	src := forwardSource(msg)

	cv.muCachedChats.RLock()
	chats := append([]client.Chat(nil), cv.cachedChats...)
	cv.muCachedChats.RUnlock()

	all := mergeForwardCandidates(chats, cv.waClient.GetContacts(),
		cv.waClient.PhoneForJID, cv.waClient.LookupName)
	visible := all

	var d dialog.Dialog
	var list *widget.List

	list = widget.NewList(
		func() int { return len(visible) },
		func() fyne.CanvasObject {
			avatarBg := canvas.NewCircle(ctpMauve)
			initials := canvas.NewText("", whiteColor)
			initials.TextStyle.Bold = true
			initials.TextSize = 16
			initials.Alignment = fyne.TextAlignCenter
			avatar := container.NewStack(avatarBg, container.NewCenter(initials))
			avatarSized := container.New(layout.NewGridWrapLayout(fyne.NewSize(40, 40)), avatar)

			nameLbl := widget.NewLabel("")
			nameLbl.TextStyle.Bold = true
			subLbl := widget.NewLabel("")
			subLbl.Importance = widget.LowImportance
			subLbl.Truncation = fyne.TextTruncateEllipsis

			text := container.NewVBox(nameLbl, subLbl)
			return container.NewBorder(nil, nil, container.NewPadded(avatarSized), nil, container.NewPadded(text))
		},
		func(id widget.ListItemID, item fyne.CanvasObject) {
			if id >= len(visible) {
				return
			}
			c := visible[id]
			row := item.(*fyne.Container)
			textPad := row.Objects[0].(*fyne.Container)
			avatarPad := row.Objects[1].(*fyne.Container)

			grid := avatarPad.Objects[0].(*fyne.Container)
			avatarStack := grid.Objects[0].(*fyne.Container)
			avatarBg := avatarStack.Objects[0].(*canvas.Circle)
			initials := avatarStack.Objects[1].(*fyne.Container).Objects[0].(*canvas.Text)

			text := textPad.Objects[0].(*fyne.Container)
			nameLbl := text.Objects[0].(*widget.Label)
			subLbl := text.Objects[1].(*widget.Label)

			nameLbl.SetText(c.Name)
			subLbl.SetText(c.Subtitle)
			avatarBg.FillColor = avatarColor(c.Name)
			avatarBg.Refresh()
			initials.Text = getInitials(c.Name)
			initials.Refresh()
		},
	)

	list.OnSelected = func(id widget.ListItemID) {
		if id >= len(visible) {
			return
		}
		target := visible[id]
		if d != nil {
			d.Hide()
		}
		dst, err := types.ParseJID(target.JID)
		if err != nil {
			dialog.ShowError(err, cv.window)
			return
		}
		go func() {
			rec, err := cv.waClient.ForwardMessage(dst, src)
			if err != nil {
				fyne.Do(func() {
					if errors.Is(err, client.ErrMediaNotDownloaded) {
						dialog.ShowInformation("Forward", "This media hasn't been downloaded yet.", cv.window)
					} else {
						dialog.ShowError(err, cv.window)
					}
				})
				return
			}
			cv.appendStoredMessage(rec)
		}()
	}

	search := widget.NewEntry()
	search.PlaceHolder = "Forward to…"
	search.OnChanged = func(q string) {
		visible = filterForwardCandidates(all, q)
		list.Refresh()
		list.UnselectAll()
	}

	scrollList := container.NewScroll(list)
	scrollList.SetMinSize(fyne.NewSize(420, 480))

	content := container.NewBorder(
		container.NewPadded(search), nil, nil, nil,
		scrollList,
	)

	d = dialog.NewCustom("Forward to", "Cancel", content, cv.window)
	d.Resize(fyne.NewSize(480, 600))
	d.Show()
	cv.window.Canvas().Focus(search)
}
