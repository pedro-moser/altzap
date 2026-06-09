package ui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"go.mau.fi/whatsmeow/types"

	"altzap/client"
)

// showChatSearch opens an in-chat search dialog. Substring match on Text
// (case-insensitive); selecting a hit closes the dialog and scrolls the
// message list to that message — expanding renderLimit if the match is
// older than the current window.
func (cv *ChatView) showChatSearch() {
	if cv.currentChatJID == "" {
		return
	}

	cv.muMessages.RLock()
	live := cv.messages[cv.currentChatJID]
	cv.muMessages.RUnlock()
	if len(live) == 0 {
		return
	}

	// Snapshot so an incoming message mid-dialog doesn't shift indices.
	snapshot := make([]*Message, len(live))
	copy(snapshot, live)

	type hit struct {
		idx int
		msg *Message
	}

	var (
		d          dialog.Dialog
		resultList *widget.List
		visible    []hit
	)

	matchCount := widget.NewLabel("")
	matchCount.Importance = widget.LowImportance

	entry := widget.NewEntry()
	entry.PlaceHolder = "Search messages…"
	entry.OnChanged = func(q string) {
		ql := strings.ToLower(strings.TrimSpace(q))
		visible = visible[:0]
		if ql != "" {
			for i, m := range snapshot {
				if m.Text == "" || m.Deleted {
					continue
				}
				if strings.Contains(strings.ToLower(m.Text), ql) {
					visible = append(visible, hit{idx: i, msg: m})
				}
			}
			matchCount.SetText(fmt.Sprintf("%d match(es)", len(visible)))
		} else {
			matchCount.SetText("")
		}
		resultList.Refresh()
		resultList.UnselectAll()
	}

	resultList = widget.NewList(
		func() int { return len(visible) },
		func() fyne.CanvasObject {
			sender := widget.NewLabel("")
			sender.TextStyle.Bold = true

			timeLabel := widget.NewLabel("")
			timeLabel.Importance = widget.LowImportance

			text := widget.NewLabel("")
			text.Truncation = fyne.TextTruncateEllipsis

			topRow := container.NewBorder(nil, nil, nil, timeLabel, sender)
			return container.NewPadded(container.NewVBox(topRow, text))
		},
		func(id widget.ListItemID, item fyne.CanvasObject) {
			if id >= len(visible) {
				return
			}
			h := visible[id]
			pad := item.(*fyne.Container)
			box := pad.Objects[0].(*fyne.Container)
			top := box.Objects[0].(*fyne.Container)
			sender := top.Objects[0].(*widget.Label)
			timeLabel := top.Objects[1].(*widget.Label)
			text := box.Objects[1].(*widget.Label)

			who := h.msg.Sender
			if h.msg.IsOwn {
				who = "You"
			}
			if who == "" {
				who = "—"
			}
			sender.SetText(who)
			text.SetText(h.msg.Text)
			timeLabel.SetText(h.msg.Timestamp.Format("02 Jan 15:04"))
		},
	)
	resultList.OnSelected = func(id widget.ListItemID) {
		if id >= len(visible) {
			return
		}
		target := visible[id].idx
		if d != nil {
			d.Hide()
		}
		cv.scrollToMessage(target)
	}

	scrollWrap := container.NewScroll(resultList)
	scrollWrap.SetMinSize(fyne.NewSize(420, 360))

	header := container.NewVBox(
		container.NewPadded(entry),
		container.NewPadded(matchCount),
	)
	content := container.NewBorder(header, nil, nil, nil, scrollWrap)

	d = dialog.NewCustom("Search in chat", "Close", content, cv.window)
	d.Resize(fyne.NewSize(480, 540))
	d.Show()
	cv.window.Canvas().Focus(entry)
}

// showChatMenu pops a menu under the ⋯ button in the chat header.
func (cv *ChatView) showChatMenu(anchor fyne.CanvasObject) {
	if cv.currentChatJID == "" {
		return
	}
	c := cv.window.Canvas()

	info := fyne.NewMenuItem("Contact info", cv.showContactInfo)
	info.Icon = theme.InfoIcon()

	archiveLabel := "Arquivar conversa"
	if parsed, err := types.ParseJID(cv.currentChatJID); err == nil &&
		cv.waClient.ChatSettings(parsed).Archived {
		archiveLabel = "Desarquivar conversa"
	}
	archive := fyne.NewMenuItem(archiveLabel, cv.toggleArchiveCurrent)
	archive.Icon = theme.FolderIcon()

	settings := fyne.NewMenuItem("Settings…", cv.showSettings)
	settings.Icon = theme.SettingsIcon()

	menu := fyne.NewMenu("",
		info,
		archive,
		fyne.NewMenuItemSeparator(),
		settings,
	)
	popup := widget.NewPopUpMenu(menu, c)
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(anchor)
	popup.ShowAtPosition(fyne.NewPos(pos.X, pos.Y+anchor.Size().Height+4))
}

// toggleArchiveCurrent flips the open chat's archived state on the server
// (app-state patch → syncs to the phone) and reloads the sidebar so the
// chat hops between the inbox and the "Arquivadas" bucket.
func (cv *ChatView) toggleArchiveCurrent() {
	jidStr := cv.currentChat()
	if jidStr == "" {
		return
	}
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return
	}
	target := !cv.waClient.ChatSettings(jid).Archived
	go func() {
		if err := cv.waClient.SetChatArchived(jid, target); err != nil {
			fyne.Do(func() { dialog.ShowError(err, cv.window) })
			return
		}
		cv.ScheduleSidebarReload()
	}()
}

// showContactInfo shows a "who is this chat" panel: large avatar, display
// name, phone/group label, full JID for power users.
func (cv *ChatView) showContactInfo() {
	if cv.currentChatJID == "" {
		return
	}
	parsed, err := types.ParseJID(cv.currentChatJID)
	if err != nil {
		return
	}

	name := cv.waClient.LookupName(parsed)
	if name == "" {
		name = parsed.User
	}

	avatarBg := canvas.NewCircle(avatarColor(name))
	initials := canvas.NewText(getInitials(name), whiteColor)
	initials.TextStyle.Bold = true
	initials.TextSize = 32
	initials.Alignment = fyne.TextAlignCenter
	photo := canvas.NewImageFromFile("")
	photo.FillMode = canvas.ImageFillContain
	photo.Hide()
	if cached := client.CachedAvatar(cv.currentChatJID); cached != "" {
		photo.File = cached
		photo.Show()
	}
	avatarStack := container.NewStack(avatarBg, container.NewCenter(initials), photo)
	avatarSized := container.New(layout.NewGridWrapLayout(fyne.NewSize(96, 96)), avatarStack)

	nameLbl := widget.NewLabel(name)
	nameLbl.TextStyle.Bold = true
	nameLbl.Alignment = fyne.TextAlignCenter

	var subtitle string
	switch {
	case parsed.Server == types.GroupServer:
		subtitle = "Group"
	default:
		if phone := cv.waClient.PhoneForJID(parsed); phone != "" {
			subtitle = "+" + phone
		} else {
			subtitle = "(unknown phone)"
		}
	}
	subLbl := widget.NewLabel(subtitle)
	subLbl.Alignment = fyne.TextAlignCenter
	subLbl.Importance = widget.MediumImportance

	jidCaption := widget.NewLabel("JID")
	jidCaption.Alignment = fyne.TextAlignCenter
	jidCaption.Importance = widget.LowImportance
	jidLbl := widget.NewLabel(cv.currentChatJID)
	jidLbl.Alignment = fyne.TextAlignCenter
	jidLbl.Wrapping = fyne.TextWrapBreak
	jidLbl.Importance = widget.LowImportance

	content := container.NewVBox(
		container.NewCenter(avatarSized),
		nameLbl,
		subLbl,
		widget.NewSeparator(),
		jidCaption,
		jidLbl,
	)

	d := dialog.NewCustom("Contact info", "Close", container.NewPadded(content), cv.window)
	d.Resize(fyne.NewSize(380, 460))
	d.Show()
}

// showSettings is the chat-menu entry for app settings. The real dialog
// (theme picker, audio devices, profile stub) lives in settings.go.
func (cv *ChatView) showSettings() {
	cv.showSettingsDialog()
}
