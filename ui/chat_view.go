package ui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"go.mau.fi/whatsmeow/types"
	"whatsappalt/client"
)

type Message struct {
	ID        string
	Sender    string
	Text      string
	Timestamp time.Time
	IsOwn     bool

	// Media (optional; MediaType == "" means plain text message)
	MediaType string
	MediaPath string // empty until download finishes
	Mimetype  string
	FileName  string
	FileSize  uint64
	Width     uint32
	Height    uint32
	Duration  uint32
	Thumb     []byte // raw JPEGThumbnail bytes for instant preview
}

// Catppuccin-derived semantic colors used by the chat UI.
var (
	sidebarBgColor   = ctpMantle
	chatBgColor      = ctpBase
	ownMsgBgColor    = ctpSurface1 // outgoing — slightly lifted off the base
	otherMsgBgColor  = ctpSurface0 // incoming — flush with the surface tier
	headerGreenColor = ctpMantle   // header reads as a darker shoulder, not loud green
	whiteColor       = ctpCrust    // for text *on* avatar circles
	emptyHintColor   = ctpOverlay1
	timeColor        = ctpOverlay0
	subtitleColor    = ctpSubtext0
	selectionTint    = color.RGBA{R: ctpMauve.R, G: ctpMauve.G, B: ctpMauve.B, A: 0x40}
)

type ChatView struct {
	fyneApp         fyne.App
	waClient        *client.WhatsAppClient
	window          fyne.Window
	searchEntry     *widget.Entry
	chatList        *widget.List
	messageBox      *fyne.Container // VBox of bubbles
	messageScroll   *container.Scroll
	messageInput    *widget.Entry
	attachBtn       *widget.Button
	micBtn          *widget.Button
	chatTitle       *widget.Label
	chatSubtitle    *widget.Label
	lastSubtitle    string
	chatArea        *fyne.Container
	chatPlaceholder fyne.CanvasObject
	chatRealView    fyne.CanvasObject
	messages        map[string][]*Message
	muMessages      sync.RWMutex
	currentChatJID  string
	cachedChats     []client.Chat
	allChats        []client.Chat
	filteredChats   []client.Chat
	isSearching     bool
	muCachedChats   sync.RWMutex
	recorder        recorder
}

// bubbleAlignLayout places a single bubble child with a pre-decided width,
// either left- or right-aligned within the row.
//
// Why fixedWidth: a widget.Label with Wrapping=TextWrapWord reports
// MinSize.Width as ~16 (only the inner padding), regardless of how wide it
// actually needs to be. So we can't ask the bubble its "natural" width
// when wrap is on — buildMessageBubble pre-measures the text without
// wrapping and bakes the chosen width into the layout.
type bubbleAlignLayout struct {
	rightAlign bool
	fixedWidth float32
}

const (
	maxBubbleWidth float32 = 600
	bubblePadding  float32 = 36 // approx. inner+outer padding around the text
	sideMargin     float32 = 14
)

func (b bubbleAlignLayout) effectiveWidth(rowWidth float32) float32 {
	w := b.fixedWidth
	if rowWidth > 0 && w > rowWidth-2*sideMargin {
		w = rowWidth - 2*sideMargin
	}
	if w < 80 {
		w = 80
	}
	return w
}

func (b bubbleAlignLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	bubble := objects[0]
	w := b.effectiveWidth(size.Width)

	bubble.Resize(fyne.NewSize(w, 0))
	h := bubble.MinSize().Height
	bubble.Resize(fyne.NewSize(w, h))

	if b.rightAlign {
		bubble.Move(fyne.NewPos(size.Width-w-sideMargin, 0))
	} else {
		bubble.Move(fyne.NewPos(sideMargin, 0))
	}
}

func (b bubbleAlignLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) == 0 {
		return fyne.NewSize(0, 0)
	}
	bubble := objects[0]
	w := b.effectiveWidth(maxBubbleWidth + 2*sideMargin) // assume enough row width
	bubble.Resize(fyne.NewSize(w, 0))
	return fyne.NewSize(w+2*sideMargin, bubble.MinSize().Height)
}

func NewChatView(fyneApp fyne.App, waClient *client.WhatsAppClient, window fyne.Window) *ChatView {
	cv := &ChatView{
		fyneApp:  fyneApp,
		waClient: waClient,
		window:   window,
		messages: make(map[string][]*Message),
	}

	cv.waClient.FetchContacts()

	// Update the in-memory Message and rebuild bubbles when an async media
	// download finishes. Runs from a download goroutine, so refresh hops
	// to the UI thread via fyne.Do.
	cv.waClient.OnMediaReady = func(chatJID, msgID, path string) {
		cv.muMessages.Lock()
		for _, m := range cv.messages[chatJID] {
			if m.ID == msgID {
				m.MediaPath = path
				break
			}
		}
		cv.muMessages.Unlock()

		if chatJID == cv.currentChatJID && cv.messageBox != nil {
			fyne.Do(func() { cv.refreshMessages() })
		}
	}

	go func() {
		// Groups need an active connection. Retry with backoff until we get
		// some — then stop, otherwise every poll re-fetches all participants
		// and whatsmeow logs a duplicate-contacts warning per call.
		gotGroups := false
		for i := 0; i < 5; i++ {
			if !gotGroups {
				if cv.waClient.FetchGroups() > 0 {
					gotGroups = true
				}
			}
			cv.loadChatList()
			cv.refreshChats()
			time.Sleep(time.Duration(i+1) * time.Second)
		}
	}()
	return cv
}

func getInitials(name string) string {
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return "?"
	}
	if len(parts) == 1 {
		return strings.ToUpper(string([]rune(parts[0])[0:1][0]))
	}
	first := []rune(parts[0])[0]
	last := []rune(parts[len(parts)-1])[0]
	return strings.ToUpper(string(first) + string(last))
}

func avatarColor(name string) color.Color {
	h := 0
	for _, c := range name {
		h = int(c) + (h << 6)
	}
	if h < 0 {
		h = -h
	}
	colors := []color.Color{
		ctpMauve, ctpBlue, ctpLavender, ctpTeal, ctpGreen,
		ctpPeach, ctpPink, ctpSapphire, ctpMaroon, ctpYellow,
	}
	return colors[h%len(colors)]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (cv *ChatView) Build() fyne.CanvasObject {
	// --- Sidebar ---
	cv.searchEntry = widget.NewEntry()
	cv.searchEntry.PlaceHolder = "Search or start new chat"
	cv.searchEntry.OnChanged = cv.onSearch

	newChatBtn := widget.NewButtonWithIcon("", theme.ContentAddIcon(), cv.onNewChat)
	newChatBtn.Importance = widget.LowImportance

	searchRow := container.NewBorder(nil, nil, nil, newChatBtn, cv.searchEntry)
	sidebarHeader := container.NewPadded(searchRow)

	cv.chatList = cv.buildChatList()
	sidebarContent := container.NewBorder(sidebarHeader, nil, nil, nil, cv.chatList)

	sidebarPanel := canvas.NewRectangle(sidebarBgColor)
	sidebarWithBg := container.NewStack(sidebarPanel, sidebarContent)

	// --- Right pane: placeholder + real chat view, swapped on selection ---
	cv.chatPlaceholder = cv.buildPlaceholder()
	cv.chatRealView = cv.buildChatRealView()

	cv.chatArea = container.NewStack(cv.chatPlaceholder)

	mainSplit := container.NewHSplit(sidebarWithBg, cv.chatArea)
	mainSplit.Offset = 0.3

	return mainSplit
}

func (cv *ChatView) buildPlaceholder() fyne.CanvasObject {
	bg := canvas.NewRectangle(ctpBase)

	icon := canvas.NewImageFromResource(theme.MailComposeIcon())
	icon.FillMode = canvas.ImageFillContain
	icon.SetMinSize(fyne.NewSize(96, 96))

	title := canvas.NewText("WhatsApp Alt", emptyHintColor)
	title.TextSize = 28
	title.TextStyle.Bold = true
	title.Alignment = fyne.TextAlignCenter

	hint := canvas.NewText("Select a chat to start messaging", emptyHintColor)
	hint.TextSize = 16
	hint.Alignment = fyne.TextAlignCenter

	content := container.NewVBox(
		layout.NewSpacer(),
		container.NewCenter(icon),
		container.NewCenter(title),
		container.NewCenter(hint),
		layout.NewSpacer(),
	)
	return container.NewStack(bg, content)
}

func (cv *ChatView) buildChatRealView() fyne.CanvasObject {
	cv.chatTitle = widget.NewLabel("")
	cv.chatTitle.TextStyle.Bold = true

	cv.chatSubtitle = widget.NewLabel("")
	cv.chatSubtitle.Importance = widget.MediumImportance

	titleBlock := container.NewVBox(cv.chatTitle, cv.chatSubtitle)

	headerRow := container.NewBorder(
		nil, nil,
		nil,
		container.NewHBox(
			widget.NewButtonWithIcon("", theme.SearchIcon(), func() {}),
			widget.NewButtonWithIcon("", theme.MoreHorizontalIcon(), func() {}),
		),
		titleBlock,
	)

	chatHeaderPanel := canvas.NewRectangle(headerGreenColor)
	chatHeader := container.NewStack(chatHeaderPanel, container.NewPadded(headerRow))

	msgScroll := cv.buildMessageArea()

	chatBgCanvas := canvas.NewRectangle(chatBgColor)
	msgArea := container.NewStack(chatBgCanvas, msgScroll)

	inputRow := cv.inputBarBuild()
	inputBg := canvas.NewRectangle(ctpMantle)
	inputBlock := container.NewStack(inputBg, container.NewPadded(inputRow))

	return container.NewBorder(chatHeader, inputBlock, nil, nil, msgArea)
}

func (cv *ChatView) buildChatList() *widget.List {
	list := widget.NewList(
		func() int { return len(cv.getChatList()) },
		func() fyne.CanvasObject {
			avatarBg := canvas.NewCircle(ctpMauve)

			initials := canvas.NewText("", whiteColor)
			initials.TextStyle.Bold = true
			initials.TextSize = 18
			initials.Alignment = fyne.TextAlignCenter

			avatar := container.NewStack(avatarBg, container.NewCenter(initials))
			avatarSized := container.New(layout.NewGridWrapLayout(fyne.NewSize(52, 52)), avatar)

			nameLabel := widget.NewLabel("")
			nameLabel.TextStyle.Bold = true

			subLabel := widget.NewLabel("")
			subLabel.Importance = widget.LowImportance
			subLabel.Truncation = fyne.TextTruncateEllipsis

			textArea := container.NewVBox(nameLabel, subLabel)

			row := container.NewBorder(nil, nil, container.NewPadded(avatarSized), nil, container.NewPadded(textArea))

			selBg := canvas.NewRectangle(color.RGBA{R: 0, G: 0, B: 0, A: 0})
			return container.NewStack(selBg, row)
		},
		func(id widget.ListItemID, item fyne.CanvasObject) {
			chats := cv.getChatList()
			if id >= len(chats) {
				return
			}
			chat := chats[id]

			// Outer is Stack(selBg, row). Row was built with
			// NewBorder(nil, nil, leftPadded, nil, centerPadded), which yields
			// Objects = [center, left] (top/bottom/right nil are skipped).
			stackC := item.(*fyne.Container)
			selBg := stackC.Objects[0].(*canvas.Rectangle)
			row := stackC.Objects[1].(*fyne.Container)

			textPad := row.Objects[0].(*fyne.Container)
			avatarPad := row.Objects[1].(*fyne.Container)

			grid := avatarPad.Objects[0].(*fyne.Container)
			avatarStack := grid.Objects[0].(*fyne.Container)
			avatarBg := avatarStack.Objects[0].(*canvas.Circle)
			initials := avatarStack.Objects[1].(*fyne.Container).Objects[0].(*canvas.Text)

			textArea := textPad.Objects[0].(*fyne.Container)
			nameLabel := textArea.Objects[0].(*widget.Label)
			subLabel := textArea.Objects[1].(*widget.Label)

			displayName := chat.DisplayName
			if displayName == "" {
				displayName = chat.JID.User
			}
			nameLabel.SetText(displayName)

			preview := chat.LastMessage
			if preview == "" {
				preview = "No messages yet"
			}
			subLabel.SetText(truncate(preview, 60))

			avatarBg.FillColor = avatarColor(displayName)
			avatarBg.Refresh()
			initials.Text = getInitials(displayName)
			initials.Color = whiteColor
			initials.Refresh()

			if cv.currentChatJID == chat.JID.String() {
				selBg.FillColor = selectionTint
			} else {
				selBg.FillColor = color.RGBA{A: 0}
			}
			selBg.Refresh()
		},
	)

	list.OnSelected = func(id widget.ListItemID) {
		chats := cv.getChatList()
		if id < len(chats) {
			cv.selectChatJID(chats[id].JID.String())
		}
	}
	return list
}

func (cv *ChatView) buildMessageArea() fyne.CanvasObject {
	cv.messageBox = container.NewVBox()
	topSpacer := canvas.NewRectangle(color.RGBA{A: 0})
	topSpacer.SetMinSize(fyne.NewSize(1, 8))
	bottomSpacer := canvas.NewRectangle(color.RGBA{A: 0})
	bottomSpacer.SetMinSize(fyne.NewSize(1, 8))
	padded := container.NewVBox(topSpacer, cv.messageBox, bottomSpacer)
	cv.messageScroll = container.NewVScroll(padded)
	return cv.messageScroll
}

func (cv *ChatView) buildMessageBubble(msg *Message) fyne.CanvasObject {
	bubbleBg := canvas.NewRectangle(otherMsgBgColor)
	if msg.IsOwn {
		bubbleBg.FillColor = ownMsgBgColor
	}
	bubbleBg.CornerRadius = 8

	parts := make([]fyne.CanvasObject, 0, 4)
	naturalContentWidth := float32(0)

	if !msg.IsOwn && msg.Sender != "" && msg.Sender != "Unknown" && msg.Sender != "<nil>" {
		senderText := canvas.NewText(msg.Sender, avatarColor(msg.Sender))
		senderText.TextStyle.Bold = true
		senderText.TextSize = 15
		parts = append(parts, senderText)
		if w := senderText.MinSize().Width; w > naturalContentWidth {
			naturalContentWidth = w
		}
	}

	switch msg.MediaType {
	case "image":
		mc := buildImageBubble(msg)
		parts = append(parts, mc)
		if w := mc.MinSize().Width; w > naturalContentWidth {
			naturalContentWidth = w
		}
	case "video":
		mc := buildVideoBubble(msg)
		parts = append(parts, mc)
		if w := mc.MinSize().Width; w > naturalContentWidth {
			naturalContentWidth = w
		}
	case "audio", "voice":
		mc := buildAudioBubble(msg)
		parts = append(parts, mc)
		if w := mc.MinSize().Width; w > naturalContentWidth {
			naturalContentWidth = w
		}
	case "document":
		mc := buildDocBubble(msg)
		parts = append(parts, mc)
		if w := mc.MinSize().Width; w > naturalContentWidth {
			naturalContentWidth = w
		}
	case "sticker":
		mc := buildStickerBubble(msg)
		parts = append(parts, mc)
		if w := mc.MinSize().Width; w > naturalContentWidth {
			naturalContentWidth = w
		}
	default:
		// Plain text. Probe natural width without wrap so we can size the
		// bubble to the text and only wrap when it would exceed maxBubbleWidth.
		if msg.Text != "" {
			probe := widget.NewLabel(msg.Text)
			textNatural := probe.MinSize().Width
			msgLabel := widget.NewLabel(msg.Text)
			if textNatural+bubblePadding > maxBubbleWidth {
				msgLabel.Wrapping = fyne.TextWrapWord
			}
			parts = append(parts, msgLabel)
			if textNatural > naturalContentWidth {
				naturalContentWidth = textNatural
			}
		}
	}

	timeText := canvas.NewText(msg.Timestamp.Format("15:04"), timeColor)
	timeText.TextSize = 12
	timeText.Alignment = fyne.TextAlignTrailing
	parts = append(parts, timeText)
	if w := timeText.MinSize().Width; w > naturalContentWidth {
		naturalContentWidth = w
	}

	bubbleInner := container.NewVBox(parts...)
	bubble := container.NewStack(bubbleBg, container.NewPadded(bubbleInner))

	bubbleW := naturalContentWidth + bubblePadding
	if bubbleW > maxBubbleWidth {
		bubbleW = maxBubbleWidth
	}

	return container.New(bubbleAlignLayout{rightAlign: msg.IsOwn, fixedWidth: bubbleW}, bubble)
}

func (cv *ChatView) refreshMessages() {
	cv.muMessages.RLock()
	msgs := cv.messages[cv.currentChatJID]
	items := make([]fyne.CanvasObject, len(msgs))
	for i, m := range msgs {
		items[i] = cv.buildMessageBubble(m)
	}
	cv.muMessages.RUnlock()

	cv.messageBox.Objects = items
	cv.messageBox.Refresh()
	cv.messageScroll.ScrollToBottom()
}

func (cv *ChatView) appendMessageBubble(msg *Message) {
	cv.messageBox.Objects = append(cv.messageBox.Objects, cv.buildMessageBubble(msg))
	cv.messageBox.Refresh()
	cv.messageScroll.ScrollToBottom()
}

func (cv *ChatView) onSearch(text string) {
	if text == "" {
		cv.muCachedChats.Lock()
		cv.isSearching = false
		cv.muCachedChats.Unlock()
		fyne.Do(func() { cv.chatList.Refresh() })
		return
	}

	queryLower := strings.ToLower(text)
	var filtered []client.Chat

	cv.muCachedChats.RLock()
	seen := make(map[string]bool)
	for _, chat := range cv.allChats {
		jidStr := chat.JID.String()
		if seen[jidStr] {
			continue
		}
		if strings.Contains(strings.ToLower(chat.DisplayName), queryLower) ||
			strings.Contains(strings.ToLower(jidStr), queryLower) {
			filtered = append(filtered, chat)
			seen[jidStr] = true
		}
	}
	cv.muCachedChats.RUnlock()

	cv.muCachedChats.Lock()
	cv.filteredChats = filtered
	cv.isSearching = true
	cv.muCachedChats.Unlock()

	fyne.Do(func() { cv.chatList.Refresh() })
}

func (cv *ChatView) loadChatList() {
	chats, err := cv.waClient.GetChats()
	if err != nil {
		chats = []client.Chat{}
	}

	// Discover any chats that exist on disk but aren't in the API result yet
	// (typical case: groups whose history was saved but FetchGroups hasn't run).
	known := make(map[string]bool, len(chats))
	for _, c := range chats {
		known[c.JID.String()] = true
	}

	storeDir := filepath.Join(".", "store")
	if entries, err := os.ReadDir(storeDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasPrefix(name, "msg_") || !strings.HasSuffix(name, ".json") {
				continue
			}
			jidStr := strings.TrimSuffix(strings.TrimPrefix(name, "msg_"), ".json")
			if known[jidStr] {
				continue
			}
			jid, err := types.ParseJID(jidStr)
			if err != nil {
				continue
			}
			if jid.Server == types.BroadcastServer {
				continue
			}
			displayName := cv.waClient.LookupName(jid)
			chats = append(chats, client.Chat{
				JID:         jid,
				DisplayName: displayName,
				IsGroup:     jid.Server == types.GroupServer,
			})
			known[jidStr] = true
		}
	}

	// Drop any status entries that snuck in from the API result.
	filtered := chats[:0]
	for _, c := range chats {
		if c.JID.Server == types.BroadcastServer {
			continue
		}
		filtered = append(filtered, c)
	}
	chats = filtered

	cv.muCachedChats.Lock()
	cv.allChats = chats
	cv.muCachedChats.Unlock()

	var activeChats []client.Chat
	for _, chat := range chats {
		jidStr := chat.JID.String()
		filename := fmt.Sprintf("msg_%s.json", jidStr)
		path := filepath.Join(storeDir, filename)

		info, err := os.Stat(path)
		if err == nil && info.Size() > 0 {
			chat.LastMessageTime = info.ModTime().Unix()
			chat.LastMessage = cv.getLastMessagePreview(path)
			if chat.DisplayName == "" {
				chat.DisplayName = cv.waClient.LookupName(chat.JID)
			}
			activeChats = append(activeChats, chat)
		}
	}

	sort.Slice(activeChats, func(i, j int) bool {
		return activeChats[i].LastMessageTime > activeChats[j].LastMessageTime
	})

	cv.muCachedChats.Lock()
	cv.cachedChats = activeChats
	cv.muCachedChats.Unlock()
}

func (cv *ChatView) getLastMessagePreview(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		return ""
	}

	var lastMsg struct {
		Text       string `json:"text"`
		FromMe     bool   `json:"from_me"`
		SenderName string `json:"sender_name,omitempty"`
		MediaType  string `json:"media_type,omitempty"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &lastMsg); err != nil {
		return ""
	}

	text := lastMsg.Text
	// Media fallback: if text/caption empty, surface the media type.
	if text == "" && lastMsg.MediaType != "" {
		text = "[" + lastMsg.MediaType + "]"
	}

	// Strip legacy "Name: text" prefix on old records (no sender_name).
	if lastMsg.SenderName == "" {
		if idx := strings.Index(text, ": "); idx >= 0 && idx < 40 {
			text = text[idx+2:]
		}
	}

	switch {
	case lastMsg.FromMe:
		text = "You: " + text
	case lastMsg.SenderName != "":
		text = lastMsg.SenderName + ": " + text
	}
	return text
}

func (cv *ChatView) getChatList() []client.Chat {
	cv.muCachedChats.RLock()
	defer cv.muCachedChats.RUnlock()

	var src []client.Chat
	if cv.isSearching {
		src = cv.filteredChats
	} else {
		src = cv.cachedChats
	}

	result := make([]client.Chat, len(src))
	copy(result, src)
	return result
}

func (cv *ChatView) refreshChats() {
	fyne.Do(func() {
		if cv.chatList != nil {
			cv.chatList.Refresh()
		}
	})
}

func (cv *ChatView) onNewChat() {
	contacts := cv.waClient.GetContacts()
	if len(contacts) == 0 {
		dialog.ShowInformation("No Contacts", "You have no contacts yet.", cv.window)
		return
	}

	// Sort contacts alphabetically by display name; named contacts first.
	sort.Slice(contacts, func(i, j int) bool {
		ni, nj := contacts[i].Name, contacts[j].Name
		if (ni == "") != (nj == "") {
			return ni != ""
		}
		return strings.ToLower(ni) < strings.ToLower(nj)
	})

	displayName := func(c client.Contact) string {
		if c.Name != "" {
			return c.Name
		}
		return c.JID.User
	}

	visible := contacts // current filtered slice

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

			name := displayName(c)
			nameLbl.SetText(name)
			subLbl.SetText("+" + c.JID.User)
			avatarBg.FillColor = avatarColor(name)
			avatarBg.Refresh()
			initials.Text = getInitials(name)
			initials.Refresh()
		},
	)
	list.OnSelected = func(id widget.ListItemID) {
		if id >= len(visible) {
			return
		}
		c := visible[id]
		if d != nil {
			d.Hide()
		}
		cv.selectChatJID(c.JID.String())
	}

	search := widget.NewEntry()
	search.PlaceHolder = "Search contacts…"
	search.OnChanged = func(q string) {
		ql := strings.ToLower(strings.TrimSpace(q))
		if ql == "" {
			visible = contacts
		} else {
			visible = visible[:0]
			for _, c := range contacts {
				name := displayName(c)
				if strings.Contains(strings.ToLower(name), ql) ||
					strings.Contains(c.JID.User, ql) {
					visible = append(visible, c)
				}
			}
			// onChanged shares the underlying array with `contacts` if we
			// didn't filter — re-slice from contacts when q is short to avoid
			// stomping the list across calls.
			fresh := make([]client.Contact, len(visible))
			copy(fresh, visible)
			visible = fresh
		}
		list.Refresh()
		list.UnselectAll()
	}

	scrollList := container.NewScroll(list)
	scrollList.SetMinSize(fyne.NewSize(420, 480))

	content := container.NewBorder(
		container.NewPadded(search), nil, nil, nil,
		scrollList,
	)

	d = dialog.NewCustom("New Chat", "Close", content, cv.window)
	d.Resize(fyne.NewSize(480, 600))
	d.Show()
	cv.window.Canvas().Focus(search)
}

func (cv *ChatView) selectChatJID(jidStr string) {
	parsed, err := types.ParseJID(jidStr)
	if err != nil {
		dialog.ShowError(fmt.Errorf("invalid JID %q: %v", jidStr, err), cv.window)
		return
	}

	cv.currentChatJID = jidStr

	cv.muMessages.Lock()
	if _, ok := cv.messages[jidStr]; !ok {
		cv.messages[jidStr] = cv.loadMessagesFromDisk(jidStr)
	}
	msgCount := len(cv.messages[jidStr])
	cv.muMessages.Unlock()

	title := cv.waClient.LookupName(parsed)
	if title == "" {
		title = jidStr
	}

	subtitle := "Messages are end-to-end encrypted"
	if parsed.Server == types.GroupServer {
		subtitle = "Group chat"
	}
	if msgCount > 0 {
		cv.muMessages.RLock()
		lastMsg := cv.messages[jidStr][msgCount-1]
		cv.muMessages.RUnlock()
		subtitle = "Last message at " + lastMsg.Timestamp.Format("02 Jan 15:04")
	}

	fyne.Do(func() {
		// Swap right pane to the real chat view if not already.
		if len(cv.chatArea.Objects) == 0 || cv.chatArea.Objects[0] != cv.chatRealView {
			cv.chatArea.Objects = []fyne.CanvasObject{cv.chatRealView}
			cv.chatArea.Refresh()
		}
		if cv.chatTitle != nil {
			cv.chatTitle.SetText(title)
		}
		if cv.chatSubtitle != nil {
			cv.chatSubtitle.SetText(subtitle)
		}
		if cv.messageBox != nil {
			cv.refreshMessages()
		}
		if cv.chatList != nil {
			cv.chatList.Refresh()
		}
	})
}

func (cv *ChatView) loadMessagesFromDisk(jid string) []*Message {
	var msgs []*Message
	filename := fmt.Sprintf("msg_%s.json", jid)
	path := filepath.Join(".", "store", filename)

	file, err := os.Open(path)
	if err != nil {
		return []*Message{}
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	for decoder.More() {
		var sm struct {
			ID         string `json:"id,omitempty"`
			ChatJID    string `json:"chat_jid"`
			SenderJID  string `json:"sender_jid"`
			SenderName string `json:"sender_name,omitempty"`
			Text       string `json:"text"`
			Timestamp  int64  `json:"timestamp"`
			FromMe     bool   `json:"from_me"`

			MediaType string `json:"media_type,omitempty"`
			MediaPath string `json:"media_path,omitempty"`
			Mimetype  string `json:"mimetype,omitempty"`
			FileName  string `json:"filename,omitempty"`
			FileSize  uint64 `json:"file_size,omitempty"`
			Width     uint32 `json:"width,omitempty"`
			Height    uint32 `json:"height,omitempty"`
			Duration  uint32 `json:"duration,omitempty"`
			ThumbB64  string `json:"thumb_b64,omitempty"`
		}
		if err := decoder.Decode(&sm); err != nil {
			continue
		}

		// Resolve sender display name. Prefer the explicit sender_name (new
		// schema). Fallback for legacy records: parse "Name: text" prefix.
		sender := ""
		text := sm.Text
		if sm.FromMe {
			sender = "You"
			if sm.SenderName == "" {
				if idx := strings.Index(text, ": "); idx >= 0 && idx < 40 {
					text = text[idx+2:]
				}
			}
		} else if sm.SenderName != "" {
			sender = sm.SenderName
		} else {
			if idx := strings.Index(text, ": "); idx >= 0 && idx < 40 {
				sender = text[:idx]
				text = text[idx+2:]
			} else if senderJID, err := types.ParseJID(sm.SenderJID); err == nil {
				sender = cv.waClient.LookupName(senderJID)
			}
		}

		var thumb []byte
		if sm.ThumbB64 != "" {
			thumb, _ = base64.StdEncoding.DecodeString(sm.ThumbB64)
		}

		msgs = append(msgs, &Message{
			ID:        sm.ID,
			Sender:    sender,
			Text:      text,
			Timestamp: time.Unix(sm.Timestamp, 0),
			IsOwn:     sm.FromMe,
			MediaType: sm.MediaType,
			MediaPath: sm.MediaPath,
			Mimetype:  sm.Mimetype,
			FileName:  sm.FileName,
			FileSize:  sm.FileSize,
			Width:     sm.Width,
			Height:    sm.Height,
			Duration:  sm.Duration,
			Thumb:     thumb,
		})
	}
	return msgs
}

func (cv *ChatView) sendMessage() {
	text := strings.TrimSpace(cv.messageInput.Text)
	if text == "" || cv.currentChatJID == "" {
		return
	}

	jid, err := types.ParseJID(cv.currentChatJID)
	if err != nil {
		dialog.ShowError(fmt.Errorf("invalid chat JID: %v", err), cv.window)
		return
	}

	if err := cv.waClient.SendMessage(jid, text); err != nil {
		dialog.ShowError(err, cv.window)
		return
	}

	newMsg := &Message{
		Sender:    "You",
		Text:      text,
		Timestamp: time.Now(),
		IsOwn:     true,
	}
	cv.muMessages.Lock()
	cv.messages[cv.currentChatJID] = append(cv.messages[cv.currentChatJID], newMsg)
	cv.muMessages.Unlock()

	cv.messageInput.SetText("")
	fyne.Do(func() {
		cv.appendMessageBubble(newMsg)
	})
}

// ReloadFromDisk drops cached messages and reloads them from store/, then
// refreshes both the sidebar (preview/order) and the open chat. Called when
// HistorySync brings in older messages.
func (cv *ChatView) ReloadFromDisk() {
	cv.muMessages.Lock()
	cv.messages = make(map[string][]*Message)
	cv.muMessages.Unlock()

	cv.loadChatList()

	fyne.Do(func() {
		if cv.chatList != nil {
			cv.chatList.Refresh()
		}
		if cv.currentChatJID != "" && cv.messageBox != nil {
			cv.muMessages.Lock()
			cv.messages[cv.currentChatJID] = cv.loadMessagesFromDisk(cv.currentChatJID)
			cv.muMessages.Unlock()
			cv.refreshMessages()
		}
	})
}

func (cv *ChatView) AddMessage(msg client.MessageEvent) {
	jidStr := msg.Info.Chat.String()

	cv.muMessages.Lock()
	if _, ok := cv.messages[jidStr]; !ok {
		cv.messages[jidStr] = cv.loadMessagesFromDisk(jidStr)
	}

	senderName := msg.SenderName
	if senderName == "" {
		if msg.Info.IsFromMe {
			senderName = "You"
		} else {
			senderName = cv.waClient.LookupName(msg.SenderJid)
		}
	}

	newMsg := &Message{
		ID:        msg.Info.ID,
		Sender:    senderName,
		Text:      msg.Text,
		Timestamp: time.Unix(msg.Timestamp, 0),
		IsOwn:     msg.Info.IsFromMe,
		MediaType: msg.MediaType,
		MediaPath: msg.MediaPath,
		Mimetype:  msg.Mimetype,
		FileName:  msg.FileName,
		FileSize:  msg.FileSize,
		Width:     msg.Width,
		Height:    msg.Height,
		Duration:  msg.Duration,
		Thumb:     msg.Thumb,
	}
	cv.messages[jidStr] = append(cv.messages[jidStr], newMsg)
	cv.muMessages.Unlock()

	fyne.Do(func() {
		if jidStr == cv.currentChatJID && cv.messageBox != nil {
			cv.appendMessageBubble(newMsg)
		}
	})
	// Refresh sidebar so new messages bump chats up the list.
	go func() {
		cv.loadChatList()
		cv.refreshChats()
	}()
}
