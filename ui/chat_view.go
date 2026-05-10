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

	"altzap/client"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"go.mau.fi/whatsmeow/types"
)

type Message struct {
	ID        string
	Sender    string
	SenderJID string // raw JID of the sender ("<user>@<server>"); needed when replying
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

// Semantic chat-UI colors. Populated by applySemanticColors() — invoked
// from ApplyPalette so they update on every theme switch.
//
// Initialization-at-declaration ("= ctpSurface1") would capture zero RGBA:
// all package-level var blocks evaluate before any init() function runs,
// so ctp* vars are still zero at that point. Result was invisible message
// bubbles because the FillColor was fully transparent.
var (
	sidebarBgColor   color.RGBA
	chatBgColor      color.RGBA
	ownMsgBgColor    color.RGBA
	otherMsgBgColor  color.RGBA
	headerGreenColor color.RGBA
	whiteColor       color.RGBA
	emptyHintColor   color.RGBA
	timeColor        color.RGBA
	subtitleColor    color.RGBA
	dateChipBgColor  color.RGBA
	selectionTint    color.RGBA
)

// applySemanticColors derives chat-UI colors from the active ctp* values.
// Bumped vs. previous mapping: incoming bubbles use Surface1 (was Surface0)
// and outgoing use Surface2 (was Surface1). Surface0 is too close to Base
// in some palettes (Tokyo Night, Macchiato), making bubbles invisible.
func applySemanticColors() {
	sidebarBgColor = ctpMantle
	chatBgColor = ctpBase
	otherMsgBgColor = ctpSurface1
	ownMsgBgColor = ctpSurface2
	headerGreenColor = ctpMantle
	whiteColor = ctpCrust
	emptyHintColor = ctpOverlay1
	timeColor = ctpOverlay0
	subtitleColor = ctpSubtext0
	dateChipBgColor = color.RGBA{R: ctpSurface0.R, G: ctpSurface0.G, B: ctpSurface0.B, A: 0xc0}
	// Bumped alpha to 0x55 — Fyne's own list-selection overlay is now off
	// (theme.ColorNameSelection → transparent), so this tint must carry
	// the entire "selected chat" signal on its own.
	selectionTint = color.RGBA{R: ctpMauve.R, G: ctpMauve.G, B: ctpMauve.B, A: 0x55}
}

type ChatView struct {
	fyneApp              fyne.App
	waClient             *client.WhatsAppClient
	window               fyne.Window
	searchEntry          *widget.Entry
	chatList             *widget.List
	messageList          *widget.List // virtualized: only visible bubbles materialized
	messageInput         *pasteAwareEntry
	attachBtn            *widget.Button
	micBtn               *widget.Button
	chatTitle            *widget.Label
	chatSubtitle         *widget.Label
	lastSubtitle         string
	headerAvatarBg       *canvas.Circle
	headerAvatarInitials *canvas.Text
	headerAvatarPhoto    *canvas.Image

	// Static-chrome canvas.Rectangles. Their FillColor is captured at
	// construction; without explicit Refresh on theme switch they keep
	// the old palette. RefreshAll re-applies the current semantic colors.
	sidebarPanel    *canvas.Rectangle
	chatHeaderPanel *canvas.Rectangle
	chatBgCanvas    *canvas.Rectangle
	inputBg         *canvas.Rectangle

	// Reply-to-message state. replyingTo == nil means the next sendMessage
	// is a fresh post; non-nil means it'll be quoted as a reply. The
	// preview elements are built once in inputBarBuild and updated by
	// beginReply/cancelReply rather than recreated each time. replyEscPop
	// is the cleanup returned by pushEsc — non-nil while a reply is
	// active, called from cancelReply (and ESC).
	replyingTo            *Message
	replyPreview          *fyne.Container
	replyPreviewSenderLbl *widget.Label
	replyPreviewTextLbl   *widget.Label
	replyPreviewAccent    *canvas.Rectangle
	replyEscPop           func()

	// Double-tap detection on the message list — Fyne's widget.List ships
	// with single-tap selection (we co-opt that path) but no built-in
	// double-tap. We track the last row tapped + when, and treat two taps
	// on the same id within a short window as a double-tap → start reply.
	lastTapID          widget.ListItemID
	lastTapTime        time.Time
	chatArea           *fyne.Container
	chatPlaceholder    fyne.CanvasObject
	chatRealView       fyne.CanvasObject
	messages           map[string][]*Message
	muMessages         sync.RWMutex
	currentChatJID     string
	currentChatIsGroup bool
	cachedChats        []client.Chat
	allChats           []client.Chat
	filteredChats      []client.Chat
	inboxChats         []client.Chat // non-archived bucket, the default sidebar view
	archivedChats      []client.Chat // archived bucket, shown only while viewingArchived
	viewingArchived    bool          // sidebar currently rendering the archived bucket
	archivedHeaderBtn  *widget.Button
	isSearching        bool
	muCachedChats      sync.RWMutex
	recorder           recorder

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

	// Reply mode (Ctrl+R): when active, replyTargetID identifies the
	// bubble currently highlighted as the quote target. replyModeEscPop
	// holds the dismiss callback registered on the ESC stack;
	// replyPrevFocused is the focusable widget that had focus before
	// reply mode took over (restored on cancel).
	replyMode        bool
	replyTargetID    string
	replyModeEscPop  func()
	replyPrevFocused fyne.Focusable

	// Search mode: searchEscPop holds the ESC dismiss for openSearch().
	searchEscPop func()

	// priorTypedRune captures the canvas's existing OnTypedRune
	// callback before installShortcuts overrides it, so non-reply-mode
	// runes still reach prior handlers.
	priorTypedRune func(rune)
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
		cv.invalidateReplyTargetIfMissing()

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
		cv.invalidateReplyTargetIfMissing()

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

// formatChatListTime renders a chat-list timestamp scaled to its age:
// today → "HH:MM", yesterday → "Yesterday", within a week → "Mon"/"Tue",
// this year → "Jan 2", older → "DD/MM/YY".
func formatChatListTime(unix int64) string {
	if unix <= 0 {
		return ""
	}
	t := time.Unix(unix, 0)
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if !t.Before(today) {
		return t.Format("15:04")
	}
	if !t.Before(today.AddDate(0, 0, -1)) {
		return "Yesterday"
	}
	if !t.Before(today.AddDate(0, 0, -6)) {
		return t.Format("Mon")
	}
	if t.Year() == now.Year() {
		return t.Format("Jan 2")
	}
	return t.Format("02/01/06")
}

// formatDateSeparator renders the centered chip between groups of messages
// from different days: "Today", "Yesterday", weekday name within a week,
// or full date.
func formatDateSeparator(t time.Time) string {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	switch {
	case day.Equal(today):
		return "Today"
	case day.Equal(today.AddDate(0, 0, -1)):
		return "Yesterday"
	case !day.Before(today.AddDate(0, 0, -6)):
		return t.Format("Monday")
	case t.Year() == now.Year():
		return t.Format("January 2")
	default:
		return t.Format("January 2, 2006")
	}
}

// sameDay reports whether a and b fall on the same calendar day in their
// respective locations.
func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func (cv *ChatView) Build() fyne.CanvasObject {
	// --- Sidebar ---
	cv.searchEntry = widget.NewEntry()
	cv.searchEntry.PlaceHolder = "Search or start new chat"
	cv.searchEntry.OnChanged = cv.onSearch
	cv.searchEntry.OnSubmitted = cv.confirmSearch

	newChatBtn := widget.NewButtonWithIcon("", theme.ContentAddIcon(), cv.onNewChat)
	newChatBtn.Importance = widget.LowImportance

	searchRow := container.NewBorder(nil, nil, nil, newChatBtn, cv.searchEntry)

	// Archived header button: starts hidden; updateArchivedHeader
	// reveals it once we know how many archived chats the user has.
	cv.archivedHeaderBtn = widget.NewButtonWithIcon("Arquivadas", theme.FolderIcon(), cv.toggleArchivedView)
	cv.archivedHeaderBtn.Alignment = widget.ButtonAlignLeading
	cv.archivedHeaderBtn.Importance = widget.LowImportance
	cv.archivedHeaderBtn.Hide()

	sidebarHeader := container.NewVBox(
		container.NewPadded(searchRow),
		cv.archivedHeaderBtn,
	)

	cv.chatList = cv.buildChatList()
	sidebarContent := container.NewBorder(sidebarHeader, nil, nil, nil, cv.chatList)

	cv.sidebarPanel = canvas.NewRectangle(sidebarBgColor)
	sidebarWithBg := container.NewStack(cv.sidebarPanel, sidebarContent)

	// --- Right pane: placeholder + real chat view, swapped on selection ---
	cv.chatPlaceholder = cv.buildPlaceholder()
	cv.chatRealView = cv.buildChatRealView()

	cv.chatArea = container.NewStack(cv.chatPlaceholder)

	mainSplit := container.NewHSplit(sidebarWithBg, cv.chatArea)
	mainSplit.Offset = 0.3

	cv.priorTypedRune = nil
	cv.installShortcuts()

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

	cv.headerAvatarBg = canvas.NewCircle(ctpMauve)
	cv.headerAvatarInitials = canvas.NewText("", whiteColor)
	cv.headerAvatarInitials.TextStyle.Bold = true
	cv.headerAvatarInitials.TextSize = 16
	cv.headerAvatarInitials.Alignment = fyne.TextAlignCenter
	cv.headerAvatarPhoto = canvas.NewImageFromFile("")
	cv.headerAvatarPhoto.FillMode = canvas.ImageFillContain
	cv.headerAvatarPhoto.Hide()
	hdrAvatar := container.NewStack(cv.headerAvatarBg, container.NewCenter(cv.headerAvatarInitials), cv.headerAvatarPhoto)
	hdrAvatarSized := container.New(layout.NewGridWrapLayout(fyne.NewSize(40, 40)), hdrAvatar)

	searchBtn := widget.NewButtonWithIcon("", theme.SearchIcon(), cv.showChatSearch)
	searchBtn.Importance = widget.LowImportance

	menuBtn := widget.NewButtonWithIcon("", theme.MoreHorizontalIcon(), nil)
	menuBtn.Importance = widget.LowImportance
	menuBtn.OnTapped = func() { cv.showChatMenu(menuBtn) }

	headerRow := container.NewBorder(
		nil, nil,
		container.NewPadded(hdrAvatarSized),
		container.NewHBox(searchBtn, menuBtn),
		titleBlock,
	)

	cv.chatHeaderPanel = canvas.NewRectangle(headerGreenColor)
	chatHeader := container.NewStack(cv.chatHeaderPanel, container.NewPadded(headerRow))

	msgScroll := cv.buildMessageArea()

	cv.chatBgCanvas = canvas.NewRectangle(chatBgColor)
	msgArea := container.NewStack(cv.chatBgCanvas, msgScroll)

	inputRow := cv.inputBarBuild()
	cv.inputBg = canvas.NewRectangle(ctpMantle)
	inputBlock := container.NewStack(cv.inputBg, container.NewPadded(inputRow))

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
			nameLabel.Truncation = fyne.TextTruncateEllipsis

			timeText := canvas.NewText("", subtitleColor)
			timeText.TextSize = 13
			timeText.Alignment = fyne.TextAlignTrailing

			topRow := container.NewBorder(nil, nil, nil, timeText, nameLabel)

			subLabel := widget.NewLabel("")
			subLabel.Importance = widget.LowImportance
			subLabel.Truncation = fyne.TextTruncateEllipsis

			// Unread badge: green circle with white count, hidden when zero.
			badgeBg := canvas.NewCircle(ctpGreen)
			badgeTextEl := canvas.NewText("", ctpCrust)
			badgeTextEl.TextStyle.Bold = true
			badgeTextEl.TextSize = 12
			badgeTextEl.Alignment = fyne.TextAlignCenter
			badgeStack := container.NewStack(badgeBg, container.NewCenter(badgeTextEl))
			badgeSized := container.New(layout.NewGridWrapLayout(fyne.NewSize(22, 22)), badgeStack)
			badgeWrap := container.NewCenter(badgeSized)

			botRow := container.NewBorder(nil, nil, nil, badgeWrap, subLabel)

			textArea := container.NewVBox(topRow, botRow)

			row := container.NewBorder(
				nil, nil,
				container.NewPadded(avatarSized),
				nil,
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
			// left=avatar, right=nil, center=text) yields Objects in order
			// [center, left] (nil slots are skipped).
			stackC := item.(*fyne.Container)
			selBg := stackC.Objects[0].(*canvas.Rectangle)
			row := stackC.Objects[1].(*fyne.Container)

			textPad := row.Objects[0].(*fyne.Container)
			avatarPad := row.Objects[1].(*fyne.Container)

			grid := avatarPad.Objects[0].(*fyne.Container)
			avatarStack := grid.Objects[0].(*fyne.Container)
			avatarBg := avatarStack.Objects[0].(*canvas.Circle)
			initials := avatarStack.Objects[1].(*fyne.Container).Objects[0].(*canvas.Text)
			photo := avatarStack.Objects[2].(*canvas.Image)

			textArea := textPad.Objects[0].(*fyne.Container)
			topRow := textArea.Objects[0].(*fyne.Container)
			botRow := textArea.Objects[1].(*fyne.Container)

			// topRow Border(left=nil, right=timeText, center=nameLabel) → [nameLabel, timeText]
			nameLabel := topRow.Objects[0].(*widget.Label)
			timeText := topRow.Objects[1].(*canvas.Text)

			// botRow Border(left=nil, right=badgeWrap, center=subLabel) → [subLabel, badgeWrap]
			subLabel := botRow.Objects[0].(*widget.Label)
			badgeWrap := botRow.Objects[1].(*fyne.Container)
			badgeSized := badgeWrap.Objects[0].(*fyne.Container)
			badgeStack := badgeSized.Objects[0].(*fyne.Container)
			badgeBg := badgeStack.Objects[0].(*canvas.Circle)
			badgeTextEl := badgeStack.Objects[1].(*fyne.Container).Objects[0].(*canvas.Text)

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

			timeText.Text = formatChatListTime(chat.LastMessageTime)
			timeText.Color = subtitleColor
			timeText.Refresh()

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
					badgeTextEl.Text = "99+"
				} else {
					badgeTextEl.Text = fmt.Sprintf("%d", n)
				}
				badgeBg.Show()
				badgeTextEl.Show()
			} else {
				badgeBg.FillColor = color.RGBA{A: 0}
				badgeTextEl.Text = ""
			}
			badgeBg.Refresh()
			badgeTextEl.Refresh()

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
	cv.lastTapID = -1

	// OnSelected fires whenever a list row is tapped (Fyne's listItem is
	// Tappable; clicks on inert bubble area land here, clicks on nested
	// buttons/audio-play stay with those buttons). Two taps on the same row
	// within doubleTapWindow trigger reply. We immediately UnselectAll
	// because List.Select is a no-op when the row is already selected, so
	// without this the second tap would silently never fire OnSelected.
	cv.messageList.OnSelected = func(id widget.ListItemID) {
		now := time.Now()
		isDouble := cv.lastTapID == id && now.Sub(cv.lastTapTime) < 500*time.Millisecond
		cv.lastTapID = id
		cv.lastTapTime = now
		cv.messageList.UnselectAll()
		if !isDouble {
			return
		}
		cv.lastTapID = -1
		// Map list ID → message index. Skip the "Load older" pseudo-row.
		cv.muMessages.RLock()
		msgs := cv.messages[cv.currentChatJID]
		start := cv.messageWindowStart(msgs)
		hasOlder := start > 0
		msgIdx := id
		if hasOlder {
			if id == 0 {
				cv.muMessages.RUnlock()
				return
			}
			msgIdx = id - 1
		}
		actual := start + msgIdx
		if actual < 0 || actual >= len(msgs) {
			cv.muMessages.RUnlock()
			return
		}
		msg := msgs[actual]
		cv.muMessages.RUnlock()
		cv.beginReply(msg)
	}
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

		// Sender name appears only on the first received message of a streak
		// in a group chat (1-1 chats hide it entirely — sender is implied by
		// alignment). 5 min gap restarts the streak.
		showSender := cv.currentChatIsGroup && !msg.IsOwn
		if showSender && actual > 0 {
			prev := msgs[actual-1]
			if !prev.IsOwn && prev.Sender == msg.Sender &&
				msg.Timestamp.Sub(prev.Timestamp) < 5*time.Minute {
				showSender = false
			}
		}

		// Date separator above the first message overall, plus whenever the
		// calendar day flips. Boundary case (actual=start with hasOlder=true):
		// the prev-in-array message exists but isn't rendered — we still
		// compare to keep the timeline correct after Load older expands.
		var dateSep string
		switch {
		case actual == 0:
			dateSep = formatDateSeparator(msg.Timestamp)
		case !sameDay(msg.Timestamp, msgs[actual-1].Timestamp):
			dateSep = formatDateSeparator(msg.Timestamp)
		}

		cv.muMessages.RUnlock()
		cacheKey = msg.ID
		bt0 := time.Now()
		content = cv.buildMessageBubble(msg, showSender, dateSep)
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

func (cv *ChatView) buildMessageBubble(msg *Message, showSender bool, dateSep string) fyne.CanvasObject {
	bubbleBg := canvas.NewRectangle(otherMsgBgColor)
	if msg.IsOwn {
		bubbleBg.FillColor = ownMsgBgColor
	}
	bubbleBg.CornerRadius = 12

	parts := make([]fyne.CanvasObject, 0, 6)
	naturalContentWidth := float32(0)

	if !msg.Deleted {
		if reply := buildReplyBox(msg); reply != nil {
			// Tap the quote preview to jump to the original message
			// (mirrors WhatsApp's behavior). No-op when the original
			// is outside the currently rendered window — Pedro can
			// hit "Load older" first, or we'll auto-expand in v2.
			var part fyne.CanvasObject = reply
			if msg.ReplyToID != "" {
				originID := msg.ReplyToID
				part = newTappableBox(reply, func() {
					cv.scrollToMessageByID(originID)
				})
			}
			parts = append(parts, part)
			if w := reply.MinSize().Width; w > naturalContentWidth {
				naturalContentWidth = w
			}
		}
	}

	if showSender && !msg.IsOwn && msg.Sender != "" && msg.Sender != "Unknown" && msg.Sender != "<nil>" {
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
			mc := buildVideoBubble(msg, cv.window)
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
			// buildMessageText routes URLs into a clickable RichText; falls back
			// to a Label when there are none (cheaper for the common case).
			if msg.Text != "" {
				probe := buildMessageText(msg.Text, false)
				textNatural := probe.MinSize().Width
				body := probe
				if textNatural+bubblePadding > maxBubbleWidth {
					body = buildMessageText(msg.Text, true)
				}
				// Wrap the body text in a Mouseable+Tappable+DoubleTappable
				// so middle-click over the text copies and double-click
				// quotes-to-reply. Wrapping the whole bubble didn't
				// dispatch — Fyne's hit test prefers the deepest matching
				// widget in DFS order; siblings inside the bubble
				// (Hyperlinks, the reaction "+" Button) match Tappable and
				// override an outer wrapper. Wrapping just the text keeps
				// the wrapper closer to the leaf.
				if cv.window != nil && !msg.Deleted {
					textCopy := msg.Text
					replyTarget := msg
					body = newClickableBubble(body,
						func() {
							if c := cv.window.Clipboard(); c != nil {
								c.SetContent(textCopy)
							}
						},
						func() { cv.beginReply(replyTarget) },
					)
				}
				parts = append(parts, body)
				if textNatural > naturalContentWidth {
					naturalContentWidth = textNatural
				}
			}
		}

		if rxn := buildReactionsRow(msg, cv); rxn != nil {
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
	stack := []fyne.CanvasObject{bubbleBg, container.NewPadded(bubbleInner)}
	if cv.replyMode && msg.ID == cv.replyTargetID {
		transparent := ctpMauve
		transparent.A = 0
		border := canvas.NewRectangle(transparent)
		border.StrokeColor = ctpMauve
		border.StrokeWidth = 2
		border.CornerRadius = 12
		stack = append(stack, border)
	}
	bubble := container.NewStack(stack...)

	bubbleW := naturalContentWidth + bubblePadding
	if bubbleW > maxBubbleWidth {
		bubbleW = maxBubbleWidth
	}

	bubbleRow := container.New(bubbleAlignLayout{rightAlign: msg.IsOwn, fixedWidth: bubbleW}, bubble)

	if dateSep != "" {
		return container.NewVBox(buildDateChip(dateSep), bubbleRow)
	}
	return bubbleRow
}

// buildDateChip is the centered "Today" / "Yesterday" / weekday / date label
// shown between groups of messages from different days.
func buildDateChip(text string) fyne.CanvasObject {
	chipText := canvas.NewText(text, subtitleColor)
	chipText.TextSize = 12
	chipText.Alignment = fyne.TextAlignCenter
	chipText.TextStyle.Bold = true

	chipBg := canvas.NewRectangle(dateChipBgColor)
	chipBg.CornerRadius = 10

	chip := container.NewStack(chipBg, container.NewPadded(chipText))
	return container.NewPadded(container.NewCenter(chip))
}

// RefreshAll forces a rebuild of message bubbles, chat-list rows, and
// static-chrome rectangles so freshly-applied palette colors show up.
// canvas.Rectangle FillColors were captured at construction; we re-apply
// the current semantic colors directly so the rest of the app follows
// the theme switch.
//
// Also clears the per-message bubble height cache: heights computed
// against the old palette can be slightly off after a switch (different
// font sizes, padding values can carry over from the theme), so a
// re-measure on next render is the safer default.
func (cv *ChatView) RefreshAll() {
	cv.muBubbleHeights.Lock()
	cv.bubbleHeights = make(map[string]float32)
	cv.muBubbleHeights.Unlock()

	repaintRect := func(r *canvas.Rectangle, c color.RGBA) {
		if r == nil {
			return
		}
		r.FillColor = c
		r.Refresh()
	}
	repaintRect(cv.sidebarPanel, sidebarBgColor)
	repaintRect(cv.chatHeaderPanel, headerGreenColor)
	repaintRect(cv.chatBgCanvas, chatBgColor)
	repaintRect(cv.inputBg, ctpMantle)

	if cv.messageList != nil {
		cv.messageList.Refresh()
	}
	if cv.chatList != nil {
		cv.chatList.Refresh()
	}
}

// refreshMessages rebuilds the visible window and either follows the new
// bottom (when the user was sitting at/near the latest message) or
// preserves their current scroll position (when they're reading older
// content). Cheap with widget.List: only the visible bubbles get
// re-materialized via UpdateItem — Length changing triggers re-layout.
//
// "Near the bottom" = within one viewport-height of the post-refresh max
// offset. This catches the common case (user has the latest visible) and
// avoids yanking the view away when they've intentionally scrolled up.
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

	preOffset := cv.messageList.GetScrollOffset()
	viewportH := cv.messageList.Size().Height

	cv.messageList.Refresh()
	cv.messageList.ScrollToBottom()

	// Both scroll calls are synchronous against widget.List's internal
	// state; only the final scroll position is rendered, so this doesn't
	// flicker. (The probe ScrollToBottom + read + maybe restore happens
	// inside one UI tick.)
	if newBottom := cv.messageList.GetScrollOffset(); newBottom-preOffset > viewportH {
		cv.messageList.ScrollToOffset(preOffset)
	}
}

// scrollToLatest unconditionally jumps to the latest message — used when
// switching chats so the user lands on the freshest content regardless of
// where they were in the previous chat.
func (cv *ChatView) scrollToLatest() {
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

// scrollToMessage expands the render window if needed to include msgIdx
// (index into cv.messages[cv.currentChatJID]) and scrolls the message list
// to that row. Map of msgIdx → widget.List ID:
//
//	id = msgIdx - start    (when no "Load older" row is showing)
//	id = msgIdx - start + 1 (when start > 0, the first row is the button)
func (cv *ChatView) scrollToMessage(msgIdx int) {
	cv.muMessages.RLock()
	n := len(cv.messages[cv.currentChatJID])
	cv.muMessages.RUnlock()
	if msgIdx < 0 || msgIdx >= n || cv.messageList == nil {
		return
	}

	if cv.renderLimit <= 0 {
		cv.renderLimit = initialRenderLimit
	}
	if needed := n - msgIdx; needed > cv.renderLimit {
		cv.renderLimit = needed + renderChunk
	}

	cv.messageList.Refresh()

	start := 0
	if n > cv.renderLimit {
		start = n - cv.renderLimit
	}
	listID := msgIdx - start
	if start > 0 {
		listID++
	}
	cv.messageList.ScrollTo(listID)
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

	activeChats = dedupeLIDPNTwins(activeChats, cv.waClient.PhoneForJID)

	// Split into "inbox" (default view) and "archived" buckets so the
	// sidebar can render only one at a time. Archive state mirrors the
	// phone via whatsmeow_chat_settings — populated lazily here
	// because it's a per-chat SQL roundtrip.
	var inboxChats, archivedChats []client.Chat
	for _, chat := range activeChats {
		if cv.waClient.IsChatArchived(chat.JID) {
			chat.Archived = true
			archivedChats = append(archivedChats, chat)
		} else {
			inboxChats = append(inboxChats, chat)
		}
	}

	cv.muCachedChats.Lock()
	cv.inboxChats = inboxChats
	cv.archivedChats = archivedChats
	if cv.viewingArchived {
		cv.cachedChats = archivedChats
	} else {
		cv.cachedChats = inboxChats
	}
	cv.muCachedChats.Unlock()

	cv.updateArchivedHeader()
}

// toggleArchivedView swaps the sidebar between the inbox and the
// archived bucket. Wired to the archivedHeaderBtn.
func (cv *ChatView) toggleArchivedView() {
	cv.muCachedChats.Lock()
	cv.viewingArchived = !cv.viewingArchived
	if cv.viewingArchived {
		cv.cachedChats = cv.archivedChats
	} else {
		cv.cachedChats = cv.inboxChats
	}
	cv.muCachedChats.Unlock()
	cv.updateArchivedHeader()
	fyne.Do(func() {
		if cv.chatList != nil {
			cv.chatList.Refresh()
		}
	})
}

// updateArchivedHeader refreshes the "Arquivadas (N)" / "Voltar"
// button label and visibility. Hidden entirely when the inbox view
// has zero archived counterparts; while viewing the archived bucket
// the button label flips to a back-affordance regardless of count.
func (cv *ChatView) updateArchivedHeader() {
	if cv.archivedHeaderBtn == nil {
		return
	}
	cv.muCachedChats.RLock()
	count := len(cv.archivedChats)
	viewing := cv.viewingArchived
	cv.muCachedChats.RUnlock()

	fyne.Do(func() {
		if viewing {
			cv.archivedHeaderBtn.SetText("← Voltar pra conversas")
			cv.archivedHeaderBtn.SetIcon(theme.NavigateBackIcon())
			cv.archivedHeaderBtn.Show()
			return
		}
		if count == 0 {
			cv.archivedHeaderBtn.Hide()
			return
		}
		cv.archivedHeaderBtn.SetText(fmt.Sprintf("Arquivadas (%d)", count))
		cv.archivedHeaderBtn.SetIcon(theme.FolderIcon())
		cv.archivedHeaderBtn.Show()
	})
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
		// LID JIDs in ContactCache come back without names from the
		// server; LookupName resolves the underlying phone-number JID
		// and returns its contact's name (or the phone, never the hash).
		return cv.waClient.LookupName(c.JID)
	}

	phoneSubtitle := func(c client.Contact) string {
		pn := cv.waClient.PhoneForJID(c.JID)
		if pn == "" {
			return ""
		}
		return "+" + pn
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
			subLbl.SetText(phoneSubtitle(c))
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

// selectNextChat opens the chat after the current one in the sidebar.
// No-op when already at the last chat (no wrap) or when the sidebar
// is empty.
func (cv *ChatView) selectNextChat() {
	cv.muCachedChats.RLock()
	target := nextChatJID(cv.cachedChats, cv.currentChatJID)
	cv.muCachedChats.RUnlock()
	if target == "" {
		return
	}
	cv.selectChatJID(target)
}

// selectPrevChat opens the chat before the current one. Symmetric to
// selectNextChat: clamps at the first chat, opens the first chat when
// none is currently open.
func (cv *ChatView) selectPrevChat() {
	cv.muCachedChats.RLock()
	target := prevChatJID(cv.cachedChats, cv.currentChatJID)
	cv.muCachedChats.RUnlock()
	if target == "" {
		return
	}
	cv.selectChatJID(target)
}

// focusComposer hands keyboard focus to the message input. When reply
// mode is active, this confirms the current target first (mirrors the
// behavior of pressing Enter in reply mode). When search is open, it
// closes search before focusing.
func (cv *ChatView) focusComposer() {
	if cv.replyMode {
		cv.confirmReplyTarget()
		return
	}
	if cv.searchEscPop != nil {
		cv.closeSearch()
	}
	if cv.window != nil && cv.messageInput != nil {
		cv.window.Canvas().Focus(cv.messageInput)
	}
}

// openSearch focuses the sidebar search entry so typing immediately
// filters the chat list. Cancels reply mode first if active. Pushes
// a dismiss handler onto the ESC stack so a single Esc closes search
// the same way it closes any other dismissible.
func (cv *ChatView) openSearch() {
	if cv.searchEntry == nil || cv.window == nil {
		return
	}
	if cv.replyMode {
		cv.exitReplyMode()
	}
	cv.window.Canvas().Focus(cv.searchEntry)
	if cv.searchEscPop == nil {
		cv.searchEscPop = pushEsc(cv.closeSearch)
	}
}

// closeSearch clears the search filter, defocuses the search entry,
// and pops its ESC-stack handler. Idempotent: safe to call when
// search isn't open (e.g. via the ESC stack dispatching multiple
// dismissibles in sequence).
func (cv *ChatView) closeSearch() {
	if cv.searchEntry == nil {
		return
	}
	cv.searchEntry.SetText("")
	if cv.window != nil && cv.messageInput != nil {
		cv.window.Canvas().Focus(cv.messageInput)
	}
	if cv.searchEscPop != nil {
		cv.searchEscPop()
		cv.searchEscPop = nil
	}
}

// confirmSearch opens the first chat in the currently-filtered sidebar
// list and hands focus to the composer. Wired as searchEntry.OnSubmitted
// in Build() so pressing Enter in the search field triggers it.
func (cv *ChatView) confirmSearch(_ string) {
	cv.muCachedChats.RLock()
	var first string
	if len(cv.cachedChats) > 0 {
		first = cv.cachedChats[0].JID.String()
	}
	cv.muCachedChats.RUnlock()
	cv.closeSearch()
	if first != "" {
		cv.selectChatJID(first)
	}
	cv.focusComposer()
}

// enterReplyMode arms the reply gesture: picks the latest non-deleted
// message in the open chat as the initial target, parks the previous
// focus so we can restore it on cancel, and pushes an ESC dismiss.
// No-op if the open chat has no replyable bubble.
func (cv *ChatView) enterReplyMode() {
	if cv.currentChatJID == "" {
		return
	}
	cv.muMessages.RLock()
	target := latestNonDeletedMessageID(cv.messages[cv.currentChatJID])
	cv.muMessages.RUnlock()
	if target == "" {
		return
	}
	if cv.replyMode {
		return // already armed
	}
	cv.replyMode = true
	cv.replyTargetID = target
	if cv.window != nil {
		cv.replyPrevFocused = cv.window.Canvas().Focused()
		cv.window.Canvas().Focus(nil)
	}
	cv.invalidateBubbleHeight(target)
	cv.refreshMessages()
	cv.scrollToReplyTarget()
	cv.replyModeEscPop = pushEsc(cv.exitReplyMode)
}

// exitReplyMode cancels reply mode without sending. Restores focus to
// whatever held it before enterReplyMode and pops the ESC handler.
// Idempotent so it's safe to call from multiple cancellation paths
// (Esc stack, chat-switch, ChatView teardown).
func (cv *ChatView) exitReplyMode() {
	if !cv.replyMode {
		return
	}
	staleTarget := cv.replyTargetID
	cv.replyMode = false
	cv.replyTargetID = ""
	if cv.replyModeEscPop != nil {
		cv.replyModeEscPop()
		cv.replyModeEscPop = nil
	}
	if cv.window != nil && cv.replyPrevFocused != nil {
		cv.window.Canvas().Focus(cv.replyPrevFocused)
	}
	cv.replyPrevFocused = nil
	cv.invalidateBubbleHeight(staleTarget)
	cv.refreshMessages()
}

// confirmReplyTarget commits the current reply target, exits reply
// mode, and focuses the composer. Same effect as pressing Enter or
// any printable character key while reply mode is armed.
func (cv *ChatView) confirmReplyTarget() {
	if !cv.replyMode {
		return
	}
	targetID := cv.replyTargetID
	cv.muMessages.RLock()
	var target *Message
	for _, m := range cv.messages[cv.currentChatJID] {
		if m.ID == targetID {
			target = m
			break
		}
	}
	cv.muMessages.RUnlock()
	cv.exitReplyMode()
	if target != nil {
		cv.beginReply(target)
	}
	if cv.window != nil && cv.messageInput != nil {
		cv.window.Canvas().Focus(cv.messageInput)
	}
}

// replyTargetNext steps the reply-mode highlight to the next newer
// non-deleted message. Clamp at the end. No-op outside reply mode.
func (cv *ChatView) replyTargetNext() {
	if !cv.replyMode {
		return
	}
	cv.muMessages.RLock()
	next := nextNonDeletedMessageID(cv.messages[cv.currentChatJID], cv.replyTargetID)
	cv.muMessages.RUnlock()
	if next == cv.replyTargetID {
		return
	}
	old := cv.replyTargetID
	cv.replyTargetID = next
	cv.invalidateBubbleHeight(old)
	cv.invalidateBubbleHeight(next)
	cv.refreshMessages()
	cv.scrollToReplyTarget()
}

// replyTargetPrev — symmetric backward step.
func (cv *ChatView) replyTargetPrev() {
	if !cv.replyMode {
		return
	}
	cv.muMessages.RLock()
	prev := prevNonDeletedMessageID(cv.messages[cv.currentChatJID], cv.replyTargetID)
	cv.muMessages.RUnlock()
	if prev == cv.replyTargetID {
		return
	}
	old := cv.replyTargetID
	cv.replyTargetID = prev
	cv.invalidateBubbleHeight(old)
	cv.invalidateBubbleHeight(prev)
	cv.refreshMessages()
	cv.scrollToReplyTarget()
}

// scrollToReplyTarget reuses scrollToMessage to bring the highlighted
// bubble into view. No-op if the target ID isn't in the current chat
// (defensive — shouldn't happen but the message list is mutable).
func (cv *ChatView) scrollToReplyTarget() {
	if cv.replyTargetID == "" {
		return
	}
	cv.muMessages.RLock()
	idx := indexOfMsg(cv.messages[cv.currentChatJID], cv.replyTargetID)
	cv.muMessages.RUnlock()
	if idx < 0 {
		return
	}
	cv.scrollToMessage(idx)
}

// scrollToMessageByID locates a message in the open chat by ID and
// scrolls the list to it. Used by the reply quote preview's tap
// handler to jump to the original message. Silent no-op if the ID is
// outside the currently rendered window — the user can hit "Load
// older" to bring older history into view first.
func (cv *ChatView) scrollToMessageByID(id string) {
	if id == "" || cv.currentChatJID == "" {
		return
	}
	cv.muMessages.RLock()
	idx := indexOfMsg(cv.messages[cv.currentChatJID], id)
	cv.muMessages.RUnlock()
	if idx < 0 {
		return
	}
	cv.scrollToMessage(idx)
}

// invalidateBubbleHeight drops the cached MinSize.Height for one
// message so the next render measures the bubble fresh. Needed because
// the reply-target border adds a couple of pixels and we don't want a
// stale height pinning the row at its pre-highlight size.
func (cv *ChatView) invalidateBubbleHeight(id string) {
	if id == "" {
		return
	}
	cv.muBubbleHeights.Lock()
	delete(cv.bubbleHeights, id)
	cv.muBubbleHeights.Unlock()
}

// invalidateReplyTargetIfMissing exits reply mode when the highlighted
// bubble is no longer in the chat's message list (e.g. after a delete
// event). Called from the OnMessageDelete / OnMessageEdit callbacks
// after the in-memory list has been mutated.
func (cv *ChatView) invalidateReplyTargetIfMissing() {
	if !cv.replyMode {
		return
	}
	cv.muMessages.RLock()
	present := indexOfMsg(cv.messages[cv.currentChatJID], cv.replyTargetID) >= 0
	cv.muMessages.RUnlock()
	if !present {
		cv.exitReplyMode()
	}
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
	cv.currentChatIsGroup = parsed.Server == types.GroupServer
	cv.renderLimit = 0 // back to the default tail size for the new chat
	cv.resetUnread(jidStr)

	cv.muMessages.Lock()
	if _, ok := cv.messages[jidStr]; !ok {
		cv.messages[jidStr] = cv.loadMessagesFromDisk(jidStr)
	}
	cv.muMessages.Unlock()

	title := cv.waClient.LookupName(parsed)
	if title == "" {
		title = jidStr
	}

	var subtitle string
	switch {
	case parsed.Server == types.GroupServer:
		subtitle = "Group"
	default:
		// PhoneForJID handles both phone-server JIDs (returns User) and LID
		// JIDs (asks the LID store for the mapped phone). LID mappings
		// populate over time, so an empty result here is a known case —
		// fall through to a blank subtitle rather than leak the opaque LID.
		if phone := cv.waClient.PhoneForJID(parsed); phone != "" {
			subtitle = "+" + phone
		}
	}

	fyne.Do(func() {
		// Swap right pane to the real chat view if not already.
		if len(cv.chatArea.Objects) == 0 || cv.chatArea.Objects[0] != cv.chatRealView {
			cv.chatArea.Objects = []fyne.CanvasObject{cv.chatRealView}
			cv.chatArea.Refresh()
		}
		// Reply state is per-chat — drop any pending reply when switching.
		cv.cancelReply()
		if cv.chatTitle != nil {
			cv.chatTitle.SetText(title)
		}
		if cv.chatSubtitle != nil {
			cv.chatSubtitle.SetText(subtitle)
		}
		if cv.headerAvatarBg != nil {
			cv.headerAvatarBg.FillColor = avatarColor(title)
			cv.headerAvatarBg.Refresh()
			cv.headerAvatarInitials.Text = getInitials(title)
			cv.headerAvatarInitials.Color = whiteColor
			cv.headerAvatarInitials.Refresh()
			if cached := client.CachedAvatar(jidStr); cached != "" {
				cv.headerAvatarPhoto.File = cached
				cv.headerAvatarPhoto.Refresh()
				cv.headerAvatarPhoto.Show()
			} else {
				cv.headerAvatarPhoto.Hide()
				cv.maybeFetchAvatar(jidStr)
			}
		}
		if cv.messageList != nil {
			cv.scrollToLatest()
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
			SenderJID:         sm.SenderJID,
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

// beginReply marks msg as the reply target for the next outgoing message
// and reveals the preview row above the input bar. Safe to call from any
// goroutine indirectly — Fyne widgets are touched here, so callers should
// invoke from the UI thread (DoubleTapped is dispatched on the UI thread
// already).
func (cv *ChatView) beginReply(msg *Message) {
	if msg == nil {
		return
	}
	cv.replyingTo = msg

	senderName := msg.Sender
	if msg.IsOwn {
		senderName = "You"
	}
	if senderName == "" {
		senderName = "—"
	}

	if cv.replyPreviewSenderLbl != nil {
		cv.replyPreviewSenderLbl.SetText(senderName)
	}
	if cv.replyPreviewTextLbl != nil {
		cv.replyPreviewTextLbl.SetText(previewForMessage(msg))
	}
	if cv.replyPreviewAccent != nil {
		cv.replyPreviewAccent.FillColor = avatarColor(senderName)
		cv.replyPreviewAccent.Refresh()
	}
	if cv.replyPreview != nil {
		cv.replyPreview.Show()
	}
	if cv.messageInput != nil {
		cv.window.Canvas().Focus(cv.messageInput)
	}
	// Replace any prior pop (in case beginReply is called without an
	// intervening cancelReply — e.g., user double-clicks a different
	// message while a reply is already pending).
	if cv.replyEscPop != nil {
		cv.replyEscPop()
	}
	cv.replyEscPop = pushEsc(cv.cancelReply)
}

// sendReaction emits an emoji reaction targeting msg. Server echoes via
// OnReactionUpdate, which replaces our in-memory Reactions list — so
// there's no optimistic local mutation here (would race with the echo
// and risk doubles). Latency is one round-trip; acceptable for a
// non-blocking single-button click.
func (cv *ChatView) sendReaction(msg *Message, emoji string) {
	if msg == nil || cv.currentChatJID == "" {
		return
	}
	chat, err := types.ParseJID(cv.currentChatJID)
	if err != nil {
		return
	}

	senderJID := msg.SenderJID
	if senderJID == "" {
		// Fallback for messages saved before SenderJID was tracked.
		if msg.IsOwn {
			if own := cv.waClient.OwnJID(); !own.IsEmpty() {
				senderJID = own.String()
			}
		} else {
			senderJID = cv.currentChatJID
		}
	}

	sender, err := types.ParseJID(senderJID)
	if err != nil {
		return
	}

	go func() {
		if err := cv.waClient.SendReaction(chat, sender, msg.ID, emoji); err != nil {
			log.Printf("send reaction (msg=%s emoji=%q): %v", msg.ID, emoji, err)
		}
	}()
}

// cancelReply drops the reply target and hides the preview row. Called
// from the X button on the preview, on send, on chat switch, and on ESC.
func (cv *ChatView) cancelReply() {
	cv.replyingTo = nil
	if cv.replyPreview != nil {
		cv.replyPreview.Hide()
	}
	if cv.replyEscPop != nil {
		cv.replyEscPop()
		cv.replyEscPop = nil
	}
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

	rt := cv.replyingTo
	var reply *client.ReplyTo
	if rt != nil {
		senderJID := rt.SenderJID
		if senderJID == "" && rt.IsOwn {
			if own := cv.waClient.OwnJID(); !own.IsEmpty() {
				senderJID = own.String()
			}
		}
		if senderJID == "" {
			// 1-1 fallback: the chat JID is the only other participant.
			senderJID = cv.currentChatJID
		}
		reply = &client.ReplyTo{
			MessageID:  rt.ID,
			SenderJID:  senderJID,
			QuotedText: previewForMessage(rt),
		}
	}

	// Optimistic send: stamp the bubble with a freshly-generated ID, place
	// it on screen, fire the whatsmeow call in the background. Drops
	// user-perceived latency from one network round-trip (often 200–500ms
	// on cellular) to ~0. GenerateMessageID returns "" only when not
	// connected — there's no path forward for a send in that case.
	msgID := cv.waClient.GenerateMessageID()
	if msgID == "" {
		dialog.ShowError(fmt.Errorf("not connected to WhatsApp"), cv.window)
		return
	}

	newMsg := &Message{
		ID:        msgID,
		Sender:    "You",
		Text:      text,
		Timestamp: time.Now(),
		IsOwn:     true,
	}
	if rt != nil {
		quotedSender := rt.Sender
		if rt.IsOwn {
			quotedSender = "You"
		}
		newMsg.ReplyToID = rt.ID
		newMsg.ReplyToSenderName = quotedSender
		newMsg.ReplyToText = previewForMessage(rt)
		newMsg.ReplyToMediaType = rt.MediaType
	}

	chatJID := cv.currentChatJID
	cv.muMessages.Lock()
	cv.messages[chatJID] = append(cv.messages[chatJID], newMsg)
	cv.muMessages.Unlock()

	cv.messageInput.SetText("")
	cv.cancelReply()
	cv.appendMessageBubble(newMsg)

	// Background send. Server echo is deduped by ID in AddMessage, so the
	// bubble doesn't double up; receipts upgrade the ✓ to ✓✓ via
	// OnMessageStatus. On failure roll the bubble back so the user knows.
	go func() {
		if _, err := cv.waClient.SendMessage(jid, text, reply, msgID); err != nil {
			log.Printf("send failed (id=%s): %v", msgID, err)
			fyne.Do(func() {
				dialog.ShowError(err, cv.window)
				cv.muMessages.Lock()
				msgs := cv.messages[chatJID]
				for i, m := range msgs {
					if m.ID == msgID {
						cv.messages[chatJID] = append(msgs[:i], msgs[i+1:]...)
						break
					}
				}
				cv.muMessages.Unlock()
				if chatJID == cv.currentChatJID && cv.messageList != nil {
					cv.refreshMessages()
				}
			})
		}
	}()
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
	log.Printf("AddMessage: chat=%s id=%s text-len=%d media=%q from-me=%v current=%s",
		jidStr, msg.Info.ID, len(msg.Text), msg.MediaType, msg.Info.IsFromMe, cv.currentChatJID)

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
				log.Printf("AddMessage: dedup hit on id=%s — skipping", msg.Info.ID)
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
		SenderJID:         msg.SenderJid.String(),
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
			log.Printf("AddMessage: appending bubble (chat-open match) id=%s", msg.Info.ID)
			cv.appendMessageBubble(newMsg)
		} else {
			log.Printf("AddMessage: bubble skipped (chat=%s vs current=%s, list=%v) id=%s",
				jidStr, cv.currentChatJID, cv.messageList != nil, msg.Info.ID)
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
