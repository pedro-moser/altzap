package ui

import (
	"encoding/base64"
	"fmt"
	"image/color"
	"log"
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
	"altzap/client"
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

	// Reply / quote (optional)
	ReplyToID         string
	ReplyToSenderName string
	ReplyToText       string
	ReplyToMediaType  string

	// Reactions (mutable: updated as reactions come in)
	Reactions []client.SavedReaction

	// Mutations after delivery (mutable)
	Edited  bool
	Deleted bool

	// Outgoing-message delivery status: "" | "delivered" | "read" | "played"
	Status string
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
	messageList     *widget.List // virtualized: only visible bubbles materialized
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

	unread          map[string]int // chat_jid -> unread count (in-memory)
	muUnread        sync.RWMutex
	notifyHook      func(senderName, chatName, preview string, isGroup bool) // optional, set by app
	totalUnreadHook func(total int)                                          // optional, for tray tooltip

	avatarFetched map[string]bool // chat_jid -> we've kicked off a fetch
	muAvatars     sync.Mutex

	// Per-message bubble height cache. widget.List's SetItemHeight triggers
	// RefreshItem (and so UpdateItem again) only when the value changes; a
	// stable cache means the second pass is a cheap no-op.
	bubbleHeights   map[string]float32
	muBubbleHeights sync.Mutex

	// Diagnostic counters for refreshMessages — number of UpdateItem
	// invocations and total wall time spent inside bubble construction
	// during a single refresh window.
	updateCalls    int
	updateBubbleNS int64
	muUpdateStats  sync.Mutex

	// Render-time limit for the open chat: 0 means default. Reset on chat
	// switch; "Load older" bumps by renderChunk.
	renderLimit int
}

const (
	initialRenderLimit = 60
	renderChunk        = 80
)

// logIfSlow prints op + elapsed only when above threshold. Lets us keep a
// permanent low-volume breadcrumb trail for chat-open hot path without
// spamming logs in the common (fast) case.
func logIfSlow(op string, t0 time.Time, threshold time.Duration) {
	if d := time.Since(t0); d >= threshold {
		log.Printf("slow %s: %s", op, d)
	}
}

// msgTypeOf returns the MediaType field, falling back to "text" for plain
// text. Used only for diagnostic logs.
func msgTypeOf(m *Message) string {
	if m == nil {
		return "<nil>"
	}
	if m.MediaType != "" {
		return m.MediaType
	}
	return "text"
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
		fyneApp:       fyneApp,
		waClient:      waClient,
		window:        window,
		messages:      make(map[string][]*Message),
		unread:        make(map[string]int),
		avatarFetched: make(map[string]bool),
		bubbleHeights: make(map[string]float32),
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

		if chatJID == cv.currentChatJID && cv.messageList != nil {
			fyne.Do(func() { cv.refreshMessages() })
		}
	}

	// Update in-memory message reactions when a ReactionMessage lands.
	cv.waClient.OnReactionUpdate = func(upd client.ReactionUpdate) {
		cv.muMessages.Lock()
		for _, m := range cv.messages[upd.ChatJID] {
			if m.ID == upd.MessageID {
				applyReactionUpdate(m, upd.Reactions)
				break
			}
		}
		cv.muMessages.Unlock()

		if upd.ChatJID == cv.currentChatJID && cv.messageList != nil {
			fyne.Do(func() { cv.refreshMessages() })
		}
	}

	// Edit: replace text in place + flag.
	cv.waClient.OnMessageEdit = func(upd client.MessageEdit) {
		cv.muMessages.Lock()
		for _, m := range cv.messages[upd.ChatJID] {
			if m.ID == upd.MessageID {
				m.Text = upd.NewText
				m.Edited = true
				break
			}
		}
		cv.muMessages.Unlock()

		if upd.ChatJID == cv.currentChatJID && cv.messageList != nil {
			fyne.Do(func() { cv.refreshMessages() })
		}
	}

	// Delete-for-everyone: flag the message; UI swaps to placeholder.
	cv.waClient.OnMessageDelete = func(upd client.MessageDelete) {
		cv.muMessages.Lock()
		for _, m := range cv.messages[upd.ChatJID] {
			if m.ID == upd.MessageID {
				m.Deleted = true
				break
			}
		}
		cv.muMessages.Unlock()

		if upd.ChatJID == cv.currentChatJID && cv.messageList != nil {
			fyne.Do(func() { cv.refreshMessages() })
		}
	}

	// Receipts: bump status (no downgrades — handled in client).
	cv.waClient.OnMessageStatus = func(upd client.MessageStatus) {
		cv.muMessages.Lock()
		for _, m := range cv.messages[upd.ChatJID] {
			if m.ID == upd.MessageID {
				m.Status = upd.Status
				break
			}
		}
		cv.muMessages.Unlock()

		if upd.ChatJID == cv.currentChatJID && cv.messageList != nil {
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

	icon := canvas.NewImageFromResource(AppIcon)
	icon.FillMode = canvas.ImageFillContain
	icon.SetMinSize(fyne.NewSize(96, 96))

	title := canvas.NewText("AltZap", emptyHintColor)
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

			// Photo layer sits on top of the colored circle. Hidden until we
			// have a downloaded avatar; UpdateItem swaps File and toggles.
			photo := canvas.NewImageFromFile("")
			photo.FillMode = canvas.ImageFillContain
			photo.Hide()

			avatar := container.NewStack(avatarBg, container.NewCenter(initials), photo)
			avatarSized := container.New(layout.NewGridWrapLayout(fyne.NewSize(52, 52)), avatar)

			nameLabel := widget.NewLabel("")
			nameLabel.TextStyle.Bold = true

			subLabel := widget.NewLabel("")
			subLabel.Importance = widget.LowImportance
			subLabel.Truncation = fyne.TextTruncateEllipsis

			textArea := container.NewVBox(nameLabel, subLabel)

			// Unread badge: green circle with white count, hidden when zero.
			badgeBg := canvas.NewCircle(ctpGreen)
			badgeText := canvas.NewText("", ctpCrust)
			badgeText.TextStyle.Bold = true
			badgeText.TextSize = 12
			badgeText.Alignment = fyne.TextAlignCenter
			badgeStack := container.NewStack(badgeBg, container.NewCenter(badgeText))
			badgeSized := container.New(layout.NewGridWrapLayout(fyne.NewSize(24, 24)), badgeStack)
			badgeWrap := container.NewCenter(badgeSized)

			row := container.NewBorder(
				nil, nil,
				container.NewPadded(avatarSized),
				container.NewPadded(badgeWrap),
				container.NewPadded(textArea),
			)

			selBg := canvas.NewRectangle(color.RGBA{R: 0, G: 0, B: 0, A: 0})
			return container.NewStack(selBg, row)
		},
		func(id widget.ListItemID, item fyne.CanvasObject) {
			chats := cv.getChatList()
			if id >= len(chats) {
				return
			}
			chat := chats[id]

			// Outer is Stack(selBg, row). Row's NewBorder(top=nil, bottom=nil,
			// left=avatar, right=badge, center=text) yields Objects in order
			// [center, left, right] (nil top/bottom are skipped).
			stackC := item.(*fyne.Container)
			selBg := stackC.Objects[0].(*canvas.Rectangle)
			row := stackC.Objects[1].(*fyne.Container)

			textPad := row.Objects[0].(*fyne.Container)
			avatarPad := row.Objects[1].(*fyne.Container)
			badgePad := row.Objects[2].(*fyne.Container)

			grid := avatarPad.Objects[0].(*fyne.Container)
			avatarStack := grid.Objects[0].(*fyne.Container)
			avatarBg := avatarStack.Objects[0].(*canvas.Circle)
			initials := avatarStack.Objects[1].(*fyne.Container).Objects[0].(*canvas.Text)
			photo := avatarStack.Objects[2].(*canvas.Image)

			textArea := textPad.Objects[0].(*fyne.Container)
			nameLabel := textArea.Objects[0].(*widget.Label)
			subLabel := textArea.Objects[1].(*widget.Label)

			badgeWrap := badgePad.Objects[0].(*fyne.Container)
			badgeSized := badgeWrap.Objects[0].(*fyne.Container)
			badgeStack := badgeSized.Objects[0].(*fyne.Container)
			badgeBg := badgeStack.Objects[0].(*canvas.Circle)
			badgeText := badgeStack.Objects[1].(*fyne.Container).Objects[0].(*canvas.Text)

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

			jidStr := chat.JID.String()
			if avatarPath := client.CachedAvatar(jidStr); avatarPath != "" {
				photo.File = avatarPath
				photo.Refresh()
				photo.Show()
			} else {
				photo.Hide()
				cv.maybeFetchAvatar(jidStr)
			}

			n := cv.unreadFor(chat.JID.String())
			if n > 0 {
				badgeBg.FillColor = ctpGreen
				if n > 99 {
					badgeText.Text = "99+"
				} else {
					badgeText.Text = fmt.Sprintf("%d", n)
				}
				badgeBg.Show()
				badgeText.Show()
			} else {
				badgeBg.FillColor = color.RGBA{A: 0}
				badgeText.Text = ""
			}
			badgeBg.Refresh()
			badgeText.Refresh()

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
	cv.messageList = widget.NewList(
		cv.messageListLength,
		cv.messageListCreate,
		cv.messageListUpdate,
	)
	cv.messageList.HideSeparators = true
	return cv.messageList
}

// messageWindowStart is the offset in cv.messages[currentChatJID] of the
// first message currently inside the render window. Anything before that is
// hidden behind the "Load older" affordance.
func (cv *ChatView) messageWindowStart(msgs []*Message) int {
	limit := cv.renderLimit
	if limit <= 0 {
		limit = initialRenderLimit
	}
	if len(msgs) > limit {
		return len(msgs) - limit
	}
	return 0
}

// messageListLength returns one row per visible message plus a leading
// "Load older" row when there are messages outside the window.
func (cv *ChatView) messageListLength() int {
	cv.muMessages.RLock()
	defer cv.muMessages.RUnlock()
	msgs := cv.messages[cv.currentChatJID]
	if len(msgs) == 0 {
		return 0
	}
	start := cv.messageWindowStart(msgs)
	rows := len(msgs) - start
	if start > 0 {
		rows++
	}
	return rows
}

// messageListCreate returns a recyclable wrapper. UpdateItem swaps in the
// real bubble; SetItemHeight sets per-row height once we know it.
//
// The placeholder MinSize is critical: widget.List computes the visible-row
// window from itemMin (the template's MinSize) BEFORE per-id heights kick
// in, so a zero-MinSize template makes Fyne think every row fits the
// viewport and call UpdateItem for all N rows on every Refresh. With a
// realistic placeholder, only ~10 visible rows materialize.
func (cv *ChatView) messageListCreate() fyne.CanvasObject {
	placeholder := canvas.NewRectangle(color.RGBA{A: 0})
	placeholder.SetMinSize(fyne.NewSize(1, 50))
	return container.NewStack(placeholder)
}

// messageListUpdate is invoked by widget.List when a row enters the
// viewport. We rebuild bubbles fresh — recycling complex bubble layouts
// (varying types, reactions, replies) is more fragile than rebuilding for
// the small set of visible rows. SetItemHeight is gated by a per-message
// height cache to dodge widget.List's recursive RefreshItem (each
// SetItemHeight with a new value calls UpdateItem again, which would
// otherwise double the bubble-build cost per visible cell).
func (cv *ChatView) messageListUpdate(id widget.ListItemID, item fyne.CanvasObject) {
	stack, ok := item.(*fyne.Container)
	if !ok {
		return
	}

	cv.muMessages.RLock()
	msgs := cv.messages[cv.currentChatJID]
	start := cv.messageWindowStart(msgs)
	hasOlder := start > 0

	var content fyne.CanvasObject
	var cacheKey string
	if hasOlder && id == 0 {
		cv.muMessages.RUnlock()
		chunk := renderChunk
		if chunk > start {
			chunk = start
		}
		olderBtn := widget.NewButton(
			fmt.Sprintf("Load %d older messages", chunk),
			cv.loadOlderMessages,
		)
		olderBtn.Importance = widget.LowImportance
		content = container.NewCenter(olderBtn)
	} else {
		msgIdx := id
		if hasOlder {
			msgIdx = id - 1
		}
		actual := start + msgIdx
		if actual < 0 || actual >= len(msgs) {
			cv.muMessages.RUnlock()
			return
		}
		msg := msgs[actual]
		cv.muMessages.RUnlock()
		cacheKey = msg.ID
		bt0 := time.Now()
		content = cv.buildMessageBubble(msg)
		bd := time.Since(bt0)
		cv.muUpdateStats.Lock()
		cv.updateBubbleNS += bd.Nanoseconds()
		cv.updateCalls++
		cv.muUpdateStats.Unlock()
		if bd >= 50*time.Millisecond {
			log.Printf("slow buildBubble id=%d type=%s text-len=%d: %s",
				id, msgTypeOf(msg), len(msg.Text), bd)
		}
	}
	stack.Objects = []fyne.CanvasObject{content}
	stack.Refresh()
	if cv.messageList != nil {
		h := content.MinSize().Height
		if cacheKey != "" {
			cv.muBubbleHeights.Lock()
			cached, hit := cv.bubbleHeights[cacheKey]
			if !hit || cached != h {
				cv.bubbleHeights[cacheKey] = h
			}
			cv.muBubbleHeights.Unlock()
		}
		cv.messageList.SetItemHeight(id, h)
	}
}

func (cv *ChatView) buildMessageBubble(msg *Message) fyne.CanvasObject {
	bubbleBg := canvas.NewRectangle(otherMsgBgColor)
	if msg.IsOwn {
		bubbleBg.FillColor = ownMsgBgColor
	}
	bubbleBg.CornerRadius = 8

	parts := make([]fyne.CanvasObject, 0, 6)
	naturalContentWidth := float32(0)

	if !msg.Deleted {
		if reply := buildReplyBox(msg); reply != nil {
			parts = append(parts, reply)
			if w := reply.MinSize().Width; w > naturalContentWidth {
				naturalContentWidth = w
			}
		}
	}

	if !msg.IsOwn && msg.Sender != "" && msg.Sender != "Unknown" && msg.Sender != "<nil>" {
		senderText := canvas.NewText(msg.Sender, avatarColor(msg.Sender))
		senderText.TextStyle.Bold = true
		senderText.TextSize = 15
		parts = append(parts, senderText)
		if w := senderText.MinSize().Width; w > naturalContentWidth {
			naturalContentWidth = w
		}
	}

	if msg.Deleted {
		placeholder := canvas.NewText("🚫 This message was deleted", emptyHintColor)
		placeholder.TextStyle.Italic = true
		placeholder.TextSize = 14
		parts = append(parts, placeholder)
		if w := placeholder.MinSize().Width; w > naturalContentWidth {
			naturalContentWidth = w
		}
	} else {
		switch msg.MediaType {
		case "image":
			mc := buildImageBubble(msg, cv.window)
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

		if rxn := buildReactionsRow(msg); rxn != nil {
			parts = append(parts, rxn)
			if w := rxn.MinSize().Width; w > naturalContentWidth {
				naturalContentWidth = w
			}
		}
	}

	timeStr := msg.Timestamp.Format("15:04")
	if msg.Edited && !msg.Deleted {
		timeStr = "edited · " + timeStr
	}
	color := timeColor
	if msg.IsOwn && !msg.Deleted {
		var check string
		switch msg.Status {
		case "delivered":
			check = "✓✓ "
		case "read", "played":
			check = "✓✓ "
			color = ctpSapphire
		default:
			check = "✓ "
		}
		timeStr = check + timeStr
	}
	timeText := canvas.NewText(timeStr, color)
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

// RefreshAll forces a rebuild of message bubbles and chat-list rows so
// freshly-applied palette colors show up. Static chrome (chat header,
// dividers, anything built once) stays with its captured colors until
// the next interaction; restarting the app picks those up cleanly.
//
// Also clears the per-message bubble height cache: heights computed
// against the old palette can be slightly off after a switch (different
// font sizes, padding values can carry over from the theme), so a
// re-measure on next render is the safer default.
func (cv *ChatView) RefreshAll() {
	cv.muBubbleHeights.Lock()
	cv.bubbleHeights = make(map[string]float32)
	cv.muBubbleHeights.Unlock()
	if cv.messageList != nil {
		cv.messageList.Refresh()
	}
	if cv.chatList != nil {
		cv.chatList.Refresh()
	}
}

// refreshMessages rebuilds the visible window and scrolls to the latest
// message. Cheap with widget.List: only the visible bubbles get
// re-materialized via UpdateItem — Length changing triggers re-layout.
func (cv *ChatView) refreshMessages() {
	t0 := time.Now()
	cv.muUpdateStats.Lock()
	cv.updateCalls = 0
	cv.updateBubbleNS = 0
	cv.muUpdateStats.Unlock()
	defer func() {
		d := time.Since(t0)
		if d >= 50*time.Millisecond {
			cv.muUpdateStats.Lock()
			calls := cv.updateCalls
			bubbleNS := cv.updateBubbleNS
			cv.muUpdateStats.Unlock()
			log.Printf("slow refreshMessages: %s (UpdateItem×%d, in-bubble %s)",
				d, calls, time.Duration(bubbleNS))
		}
	}()
	if cv.messageList == nil {
		return
	}
	cv.messageList.Refresh()
	cv.messageList.ScrollToBottom()
}

// loadOlderMessages expands the render window by renderChunk without
// scrolling — keeps the user's current view stable as older content
// reveals above.
func (cv *ChatView) loadOlderMessages() {
	if cv.renderLimit <= 0 {
		cv.renderLimit = initialRenderLimit
	}
	cv.renderLimit += renderChunk
	if cv.messageList != nil {
		cv.messageList.Refresh()
	}
}

func (cv *ChatView) appendMessageBubble(_ *Message) {
	if cv.messageList == nil {
		return
	}
	cv.messageList.Refresh()
	cv.messageList.ScrollToBottom()
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

	known := make(map[string]bool, len(chats))
	for _, c := range chats {
		known[c.JID.String()] = true
	}

	summaries, err := cv.waClient.ChatSummaries()
	if err != nil {
		log.Printf("ChatSummaries: %v", err)
	}

	// Discover chats present in the DB but missing from the API result
	// (typical: groups whose history was saved but FetchGroups hasn't run).
	for _, sum := range summaries {
		if known[sum.ChatJID] {
			continue
		}
		jid, err := types.ParseJID(sum.ChatJID)
		if err != nil {
			continue
		}
		if jid.Server == types.BroadcastServer {
			continue
		}
		chats = append(chats, client.Chat{
			JID:         jid,
			DisplayName: cv.waClient.LookupName(jid),
			IsGroup:     jid.Server == types.GroupServer,
		})
		known[sum.ChatJID] = true
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

	summaryByJID := make(map[string]client.ChatSummary, len(summaries))
	for _, s := range summaries {
		summaryByJID[s.ChatJID] = s
	}

	var activeChats []client.Chat
	for _, chat := range chats {
		sum, ok := summaryByJID[chat.JID.String()]
		if !ok {
			continue
		}
		chat.LastMessageTime = sum.LastTimestamp
		chat.LastMessage = formatLastMessagePreview(sum)
		if chat.DisplayName == "" {
			chat.DisplayName = cv.waClient.LookupName(chat.JID)
		}
		activeChats = append(activeChats, chat)
	}

	sort.Slice(activeChats, func(i, j int) bool {
		return activeChats[i].LastMessageTime > activeChats[j].LastMessageTime
	})

	cv.muCachedChats.Lock()
	cv.cachedChats = activeChats
	cv.muCachedChats.Unlock()
}

// formatLastMessagePreview turns a ChatSummary into the sidebar's preview
// line. Mirrors the previous getLastMessagePreview formatting (legacy
// "Name: " prefix stripping, "[mediatype]" fallback, "You: " / "Sender: "
// prefix).
func formatLastMessagePreview(s client.ChatSummary) string {
	text := s.LastText
	if text == "" && s.LastMediaType != "" {
		text = "[" + s.LastMediaType + "]"
	}
	if s.LastSenderName == "" {
		if idx := strings.Index(text, ": "); idx >= 0 && idx < 40 {
			text = text[idx+2:]
		}
	}
	switch {
	case s.LastFromMe:
		text = "You: " + text
	case s.LastSenderName != "":
		text = s.LastSenderName + ": " + text
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
	t0 := time.Now()
	defer logIfSlow("selectChatJID "+jidStr, t0, 100*time.Millisecond)

	parsed, err := types.ParseJID(jidStr)
	if err != nil {
		dialog.ShowError(fmt.Errorf("invalid JID %q: %v", jidStr, err), cv.window)
		return
	}

	cv.currentChatJID = jidStr
	cv.renderLimit = 0 // back to the default tail size for the new chat
	cv.resetUnread(jidStr)

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
		if cv.messageList != nil {
			cv.refreshMessages()
		}
		if cv.chatList != nil {
			cv.chatList.Refresh()
		}
	})
}

func (cv *ChatView) loadMessagesFromDisk(jid string) []*Message {
	t0 := time.Now()
	defer func() { logIfSlow("loadMessagesFromDisk "+jid, t0, 50*time.Millisecond) }()
	recs, err := cv.waClient.LoadMessages(jid)
	if err != nil {
		return []*Message{}
	}
	msgs := make([]*Message, 0, len(recs))
	for _, sm := range recs {
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
			ID:                sm.ID,
			Sender:            sender,
			Text:              text,
			Timestamp:         time.Unix(sm.Timestamp, 0),
			IsOwn:             sm.FromMe,
			MediaType:         sm.MediaType,
			MediaPath:         sm.MediaPath,
			Mimetype:          sm.Mimetype,
			FileName:          sm.FileName,
			FileSize:          sm.FileSize,
			Width:             sm.Width,
			Height:            sm.Height,
			Duration:          sm.Duration,
			Thumb:             thumb,
			ReplyToID:         sm.ReplyToID,
			ReplyToSenderName: sm.ReplyToSenderName,
			ReplyToText:       sm.ReplyToText,
			ReplyToMediaType:  sm.ReplyToMediaType,
			Reactions:         sm.Reactions,
			Edited:            sm.Edited,
			Deleted:           sm.Deleted,
			Status:            sm.Status,
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

	msgID, err := cv.waClient.SendMessage(jid, text)
	if err != nil {
		dialog.ShowError(err, cv.window)
		return
	}

	newMsg := &Message{
		ID:        msgID,
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
		if cv.currentChatJID != "" && cv.messageList != nil {
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

	// Dedup: skip if we already have this message ID (e.g. our own outgoing
	// message we appended optimistically, now echoed back by whatsmeow).
	if msg.Info.ID != "" {
		for _, m := range cv.messages[jidStr] {
			if m.ID == msg.Info.ID {
				cv.muMessages.Unlock()
				return
			}
		}
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
		ID:                msg.Info.ID,
		Sender:            senderName,
		Text:              msg.Text,
		Timestamp:         time.Unix(msg.Timestamp, 0),
		IsOwn:             msg.Info.IsFromMe,
		MediaType:         msg.MediaType,
		MediaPath:         msg.MediaPath,
		Mimetype:          msg.Mimetype,
		FileName:          msg.FileName,
		FileSize:          msg.FileSize,
		Width:             msg.Width,
		Height:            msg.Height,
		Duration:          msg.Duration,
		Thumb:             msg.Thumb,
		ReplyToID:         msg.ReplyToID,
		ReplyToSenderName: msg.ReplyToSenderName,
		ReplyToText:       msg.ReplyToText,
		ReplyToMediaType:  msg.ReplyToMediaType,
	}
	cv.messages[jidStr] = append(cv.messages[jidStr], newMsg)
	cv.muMessages.Unlock()

	// Unread + notification: only when this chat isn't on screen and the
	// message isn't from us. Cheap heuristic — no window-focus detection.
	if !msg.Info.IsFromMe && jidStr != cv.currentChatJID {
		cv.incrementUnread(jidStr)
		if cv.notifyHook != nil {
			chatName := cv.waClient.LookupName(msg.Info.Chat)
			cv.notifyHook(senderName, chatName, previewForMessage(newMsg), msg.Info.IsGroup)
		}
	}

	fyne.Do(func() {
		if jidStr == cv.currentChatJID && cv.messageList != nil {
			cv.appendMessageBubble(newMsg)
		}
	})
	// Refresh sidebar so new messages bump chats up the list.
	go func() {
		cv.loadChatList()
		cv.refreshChats()
	}()
}

// previewForMessage returns a short string suitable for sidebar previews and
// notification bodies. Falls back to media-type tag when there's no text.
func previewForMessage(m *Message) string {
	if m.Text != "" {
		return m.Text
	}
	switch m.MediaType {
	case "image":
		return "📷 Photo"
	case "video":
		return "🎬 Video"
	case "audio":
		return "🎵 Audio"
	case "voice":
		return "🎤 Voice message"
	case "document":
		if m.FileName != "" {
			return "📎 " + m.FileName
		}
		return "📎 Document"
	case "sticker":
		return "🎨 Sticker"
	}
	return ""
}

func (cv *ChatView) incrementUnread(chatJID string) {
	cv.muUnread.Lock()
	cv.unread[chatJID]++
	cv.muUnread.Unlock()
	cv.fireTotalUnread()
}

func (cv *ChatView) resetUnread(chatJID string) {
	cv.muUnread.Lock()
	delete(cv.unread, chatJID)
	cv.muUnread.Unlock()
	cv.fireTotalUnread()
}

func (cv *ChatView) unreadFor(chatJID string) int {
	cv.muUnread.RLock()
	defer cv.muUnread.RUnlock()
	return cv.unread[chatJID]
}

// TotalUnread returns the sum of unread messages across all chats — used for
// the system tray tooltip.
func (cv *ChatView) TotalUnread() int {
	cv.muUnread.RLock()
	defer cv.muUnread.RUnlock()
	total := 0
	for _, n := range cv.unread {
		total += n
	}
	return total
}

func (cv *ChatView) fireTotalUnread() {
	if cv.totalUnreadHook != nil {
		cv.totalUnreadHook(cv.TotalUnread())
	}
}

// SetNotifyHook installs a callback fired when an incoming message warrants a
// desktop notification. Set to nil to disable.
func (cv *ChatView) SetNotifyHook(fn func(senderName, chatName, preview string, isGroup bool)) {
	cv.notifyHook = fn
}

// SetTotalUnreadHook installs a callback fired whenever the total unread count
// changes (chat received message or was opened). Used for tray tooltip.
func (cv *ChatView) SetTotalUnreadHook(fn func(total int)) {
	cv.totalUnreadHook = fn
}

// maybeFetchAvatar kicks off an async profile picture download for the given
// JID, but only the first time per session. EnsureAvatar dedupes inflight
// requests internally; this UI-side cache avoids spawning a goroutine on
// every chat-list render.
func (cv *ChatView) maybeFetchAvatar(jidStr string) {
	cv.muAvatars.Lock()
	if cv.avatarFetched[jidStr] {
		cv.muAvatars.Unlock()
		return
	}
	cv.avatarFetched[jidStr] = true
	cv.muAvatars.Unlock()

	go func() {
		if path := cv.waClient.EnsureAvatar(jidStr); path != "" {
			fyne.Do(func() {
				if cv.chatList != nil {
					cv.chatList.Refresh()
				}
			})
		}
	}()
}
