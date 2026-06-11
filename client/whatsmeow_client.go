package client

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

// SavedReaction is one user's reaction to a message. WhatsApp allows at most
// one reaction per user per message; setting an empty emoji removes it.
type SavedReaction struct {
	Emoji      string `json:"emoji"`
	SenderJID  string `json:"sender_jid"`
	SenderName string `json:"sender_name,omitempty"`
	Timestamp  int64  `json:"timestamp"`
}

// SavedMessage is the on-disk JSON record for a chat message. New fields are
// all optional so legacy records (text-only, prefixed sender) keep decoding.
type SavedMessage struct {
	ID         string `json:"id,omitempty"`
	ChatJID    string `json:"chat_jid"`
	SenderJID  string `json:"sender_jid"`
	SenderName string `json:"sender_name,omitempty"` // PushName at save time, no in-text prefix
	Text       string `json:"text"`                  // body text or caption
	Timestamp  int64  `json:"timestamp"`
	FromMe     bool   `json:"from_me"`

	// Media (optional). When MediaType != "", treat as a media message.
	MediaType string `json:"media_type,omitempty"` // image|video|audio|voice|document|sticker
	MediaPath string `json:"media_path,omitempty"` // relative path; "" until downloaded
	Mimetype  string `json:"mimetype,omitempty"`
	FileName  string `json:"filename,omitempty"`
	FileSize  uint64 `json:"file_size,omitempty"`
	Width     uint32 `json:"width,omitempty"`
	Height    uint32 `json:"height,omitempty"`
	Duration  uint32 `json:"duration,omitempty"`  // seconds
	ThumbB64  string `json:"thumb_b64,omitempty"` // JPEGThumbnail base64 for instant preview

	// Forwarded mirrors ContextInfo.IsForwarded (the "Forwarded" tag);
	// GifPlayback marks a VideoMessage that official clients render as a
	// looping GIF rather than a regular video.
	Forwarded   bool `json:"forwarded,omitempty"`
	GifPlayback bool `json:"gif_playback,omitempty"`

	// Reply / quote (optional). Non-empty ReplyToID = this message quotes another.
	ReplyToID         string `json:"reply_to_id,omitempty"`
	ReplyToSenderJID  string `json:"reply_to_sender_jid,omitempty"`
	ReplyToSenderName string `json:"reply_to_sender_name,omitempty"`
	ReplyToText       string `json:"reply_to_text,omitempty"`
	ReplyToMediaType  string `json:"reply_to_media_type,omitempty"`

	// Reactions, accumulated as users react. Persisted across restarts.
	Reactions []SavedReaction `json:"reactions,omitempty"`

	// Mutations after delivery (edited / deleted-for-everyone).
	Edited    bool  `json:"edited,omitempty"`
	EditedAt  int64 `json:"edited_at,omitempty"`
	Deleted   bool  `json:"deleted,omitempty"`
	DeletedAt int64 `json:"deleted_at,omitempty"`

	// Delivery status for outgoing messages (incoming = always "").
	// Progresses one-way: "" -> "delivered" -> "read" (or "played" for voice).
	Status string `json:"status,omitempty"`
}

type LoginCallback func()

// MessageEvent represents an incoming message handed off to the UI layer.
type MessageEvent struct {
	Info       types.MessageInfo
	Text       string
	SenderName string
	SenderJid  types.JID
	Timestamp  int64
	IsGroup    bool

	// Media (optional)
	MediaType string
	MediaPath string // "" until download finishes
	Mimetype  string
	FileName  string
	FileSize  uint64
	Width     uint32
	Height    uint32
	Duration  uint32
	Thumb     []byte // raw JPEGThumbnail bytes

	// Forwarded mirrors ContextInfo.IsForwarded; GifPlayback marks a
	// VideoMessage rendered as a looping GIF by official clients.
	Forwarded   bool
	GifPlayback bool

	// Reply / quote (optional)
	ReplyToID         string
	ReplyToSenderJID  string
	ReplyToSenderName string
	ReplyToText       string
	ReplyToMediaType  string
}

// ReactionUpdate carries a single reaction-change for the UI to refresh in place.
type ReactionUpdate struct {
	ChatJID   string
	MessageID string
	Reactions []SavedReaction // current full list for that message after the update
}

// MessageEdit signals an in-place text update for an existing message.
type MessageEdit struct {
	ChatJID   string
	MessageID string
	NewText   string
	EditedAt  int64
}

// MessageDelete signals a "delete for everyone" applied to an existing message.
type MessageDelete struct {
	ChatJID   string
	MessageID string
	DeletedAt int64
}

// MessageStatus signals an outgoing message's progress: delivered/read/played.
type MessageStatus struct {
	ChatJID   string
	MessageID string
	Status    string // "delivered" | "read" | "played"
}

// Contact represents a contact in the user's contact list
type Contact struct {
	JID        types.JID
	Name       string
	ShortName  string
	UpdateTime int64
}

// displayNameFromContactInfo returns the contact's *saved* address-book name
// (full/first only). It excludes BusinessName and PushName (network-provided
// names tracked in pushNameCache) and RedactedPhone (a masked phone, never a
// name) — so nameless @lid entries stay empty and LID→PN resolution can run,
// and a stale DB push name never outranks a fresher live one.
func displayNameFromContactInfo(info types.ContactInfo) string {
	for _, name := range []string{
		info.FullName,
		info.FirstName,
	} {
		if strings.TrimSpace(name) != "" {
			return name
		}
	}
	return ""
}

// Chat represents an active chat
type Chat struct {
	JID             types.JID
	DisplayName     string
	LastMessage     string
	LastMessageTime int64
	IsGroup         bool
	AvatarURL       string
	// Archived/Pinned/Muted mirror whatsmeow's chat_settings (synced from
	// the phone via app_state / history sync). Read-only in v1 — toggling
	// from AltZap comes later. Muted is pre-resolved against the sidebar
	// load instant since MutedUntil may be "forever" or already expired.
	Archived bool
	Pinned   bool
	Muted    bool
}

// WhatsAppClient wraps the whatsmeow client with higher-level operations
type WhatsAppClient struct {
	client            *whatsmeow.Client
	store             *sqlstore.Container
	msgStore          *MessageStore
	OnMessage         func(MessageEvent)
	OnLogin           LoginCallback
	OnHistoryUpdate   func()
	OnConnected       func()                                 // fires on every (re)connection — use for UI refresh
	OnMediaReady      func(chatJID, msgID, mediaPath string) // fires when an async download finishes
	OnReactionUpdate  func(ReactionUpdate)                   // fires when a reaction is added/removed
	OnMessageEdit     func(MessageEdit)                      // fires when a message's text was edited
	OnMessageDelete   func(MessageDelete)                    // fires on "delete for everyone"
	OnMessageStatus   func(MessageStatus)                    // fires when delivered/read receipt arrives
	OnContactsUpdated func()                                 // fires after a background contact-cache refresh
	OnChatMarkedRead  func(chatJID string)                   // fires when the chat was read on another device (phone)
	// OnChatSettingsChanged fires when archive/pin/mute state changed on the
	// phone. May run on whatsmeow's event goroutine — handlers must not block.
	OnChatSettingsChanged func()
	muChannels            sync.RWMutex
	// ContactCache holds *saved* address-book names (full/first only),
	// owned exclusively by FetchContacts — safe to replace wholesale.
	// pushNameCache holds network-provided display names (push/business),
	// learned from events and updatable. Both are guarded by muContacts.
	// Splitting them lets FetchContacts rebuild saved names without wiping a
	// push name, and lets the resolver rank saved > push.
	ContactCache  map[string]Contact
	pushNameCache map[string]string
	muContacts    sync.RWMutex
	muMessages    sync.Mutex
	messages      map[string][]MessageEvent
	chatRegistry  map[string]string // jid -> display name
	muChats       sync.RWMutex
	groupCache    map[string]string // jid -> group name
	muGroups      sync.RWMutex

	// lidPNCache maps a LID JID string to the underlying phone-number JID
	// it resolves to. Populated lazily by lookupPNForLID hitting whatsmeow's
	// LIDs store; existing entries are shared across LookupName calls so we
	// avoid a SQL round-trip per message render.
	lidPNCache map[string]types.JID
	muLIDs     sync.RWMutex

	// contactRefreshTimer coalesces bursty contact events (events.Contact can
	// fire once per contact during sync) into a single background FetchContacts.
	muContactRefresh    sync.Mutex
	contactRefreshTimer *time.Timer

	// chatSettingsCache memoizes phone-synced per-chat flags (archive/pin/
	// mute). The sidebar consults one entry per row per reload — uncached
	// that's one SQL roundtrip each. Entries drop on app-state events.
	chatSettingsCache map[string]ChatSettingsInfo
	muChatSettings    sync.RWMutex
}

// NewWhatsAppClient creates a new WhatsApp client instance.
// msgStore is required — chat history persistence goes through it.
func NewWhatsAppClient(clientStore *sqlstore.Container, msgStore *MessageStore) *WhatsAppClient {
	wa := &WhatsAppClient{
		msgStore:          msgStore,
		ContactCache:      make(map[string]Contact),
		pushNameCache:     make(map[string]string),
		messages:          make(map[string][]MessageEvent),
		chatRegistry:      make(map[string]string),
		groupCache:        make(map[string]string),
		lidPNCache:        make(map[string]types.JID),
		chatSettingsCache: make(map[string]ChatSettingsInfo),
	}

	device, err := clientStore.GetFirstDevice(context.Background())
	if err != nil {
		log.Printf("Warning: could not get device: %v", err)
		return wa
	}

	wa.store = clientStore
	// ERROR-only logger silences the chatty "duplicate contacts" / "duplicate
	// LID" WARNs whatsmeow emits when the same person is in multiple groups.
	wa.client = whatsmeow.NewClient(device, waLog.Stdout("WAClient", "ERROR", false))
	wa.client.EnableAutoReconnect = true
	wa.client.AddEventHandler(wa.eventHandler)
	wa.client.AddEventHandler(func(evt any) {
		switch v := evt.(type) {
		case *events.PairSuccess:
			log.Printf("Pair successful: %s, Business: %s, Platform: %s", v.ID, v.BusinessName, v.Platform)
			if wa.client.Store.ID != nil {
				log.Println("Device store has been populated - login complete")
			}
			if wa.OnLogin != nil {
				wa.OnLogin()
			}
		case *events.Connected:
			log.Println("WhatsApp connected")
			wa.scheduleContactRefresh()
			if wa.OnConnected != nil {
				wa.OnConnected()
			}
		case *events.OfflineSyncCompleted:
			log.Printf("Offline sync completed: %d events", v.Count)
			wa.scheduleContactRefresh()
			if wa.OnHistoryUpdate != nil {
				wa.OnHistoryUpdate()
			}
		case *events.Contact:
			wa.scheduleContactRefresh()
		case *events.PushName:
			wa.rememberPushName(v.JID, v.NewPushName)
			if !v.JIDAlt.IsEmpty() {
				wa.rememberPushName(v.JIDAlt, v.NewPushName)
			}
		case *events.BusinessName:
			wa.rememberBusinessName(v.JID, v.NewBusinessName)
		case *events.KeepAliveTimeout:
			log.Printf("KeepAlive timeout #%d (last success: %s ago)",
				v.ErrorCount, time.Since(v.LastSuccess).Round(time.Second))
			if v.ErrorCount >= 3 {
				log.Println("KeepAlive: forcing reconnect")
				wa.client.Disconnect()
			}
		case *events.KeepAliveRestored:
			log.Println("KeepAlive restored")
		case *events.LoggedOut:
			log.Println("Logged out")
			wa.client.Store.ID = nil
		case *events.HistorySync:
			wa.handleHistorySync(v)
		case *events.Receipt:
			wa.handleReceipt(v)
		case *events.MarkChatAsRead:
			// The phone (or another device) read this chat — its receipts
			// supersede anything we'd send, so let the UI drop local unread
			// state. The "marked as unread" direction is ignored: AltZap has
			// no manual-unread concept yet.
			if v.Action.GetRead() && wa.OnChatMarkedRead != nil {
				wa.OnChatMarkedRead(v.JID.String())
			}
		case *events.Archive:
			wa.invalidateChatSettings(v.JID)
		case *events.Pin:
			wa.invalidateChatSettings(v.JID)
		case *events.Mute:
			wa.invalidateChatSettings(v.JID)
		}
	})

	return wa
}

// Connect connects to WhatsApp servers
func (w *WhatsAppClient) Connect() error {
	if w.client == nil {
		return fmt.Errorf("client not initialized")
	}

	if err := w.client.Connect(); err != nil {
		return fmt.Errorf("failed to connect to WhatsApp: %w", err)
	}
	return nil
}

// Disconnect disconnects from WhatsApp servers
func (w *WhatsAppClient) Disconnect() {
	if w.client != nil && w.client.IsConnected() {
		w.client.Disconnect()
	}
}

// IsConnected returns whether the client is connected
func (w *WhatsAppClient) IsConnected() bool {
	if w.client == nil {
		return false
	}
	return w.client.IsConnected()
}

// IsLoggedIn returns whether the user is logged in
func (w *WhatsAppClient) IsLoggedIn() bool {
	if w.client == nil {
		return false
	}
	return w.client.Store.ID != nil
}

// GetQRChannel generates a QR code for login
// Returns the raw whatsmeow QR channel. Callers should read from the channel
// and check item.Event, item.Error for status.
func (w *WhatsAppClient) GetQRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	ch, err := w.client.GetQRChannel(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get QR channel: %w", err)
	}

	return ch, nil
}

// WaitUntilLoggedIn returns whether the user is logged in
func (w *WhatsAppClient) WaitUntilLoggedIn() bool {
	for i := 0; i < 120; i++ { // Wait up to 60 seconds (checking every 500ms)
		if w.IsLoggedIn() {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// extractContext returns the ContextInfo of whichever message variant is set.
// ContextInfo carries reply (QuotedMessage), mentions, and forward state.
func extractContext(m *waE2E.Message) *waE2E.ContextInfo {
	if m == nil {
		return nil
	}
	switch {
	case m.ExtendedTextMessage != nil:
		return m.ExtendedTextMessage.GetContextInfo()
	case m.ImageMessage != nil:
		return m.ImageMessage.GetContextInfo()
	case m.VideoMessage != nil:
		return m.VideoMessage.GetContextInfo()
	case m.AudioMessage != nil:
		return m.AudioMessage.GetContextInfo()
	case m.DocumentMessage != nil:
		return m.DocumentMessage.GetContextInfo()
	case m.StickerMessage != nil:
		return m.StickerMessage.GetContextInfo()
	}
	return nil
}

// extractReply pulls quote metadata from a ContextInfo. Returns empty values
// when the message isn't a reply.
func extractReply(ctx *waE2E.ContextInfo) (id, senderJID, senderName, text, mediaType string) {
	if ctx == nil {
		return
	}
	quoted := ctx.GetQuotedMessage()
	if quoted == nil {
		return
	}
	id = ctx.GetStanzaID()
	senderJID = ctx.GetParticipant()

	text = extractText(quoted)
	mt, _, _, _, _, _, _, _, caption := extractMedia(quoted)
	mediaType = mt
	if text == "" {
		text = caption
	}
	return
}

// extractMedia inspects a whatsmeow Message proto and returns media metadata
// suitable for persistence and UI rendering. mediaType is "" for plain text.
func extractMedia(m *waE2E.Message) (mediaType, mime, fileName string, size uint64, w, h, dur uint32, thumb []byte, caption string) {
	if m == nil {
		return
	}
	switch {
	case m.ImageMessage != nil:
		im := m.ImageMessage
		mediaType = "image"
		mime = im.GetMimetype()
		size = im.GetFileLength()
		w = im.GetWidth()
		h = im.GetHeight()
		thumb = im.GetJPEGThumbnail()
		caption = im.GetCaption()
	case m.VideoMessage != nil:
		vm := m.VideoMessage
		mediaType = "video"
		mime = vm.GetMimetype()
		size = vm.GetFileLength()
		w = vm.GetWidth()
		h = vm.GetHeight()
		dur = vm.GetSeconds()
		thumb = vm.GetJPEGThumbnail()
		caption = vm.GetCaption()
	case m.AudioMessage != nil:
		am := m.AudioMessage
		if am.GetPTT() {
			mediaType = "voice"
		} else {
			mediaType = "audio"
		}
		mime = am.GetMimetype()
		size = am.GetFileLength()
		dur = am.GetSeconds()
	case m.DocumentMessage != nil:
		dm := m.DocumentMessage
		mediaType = "document"
		mime = dm.GetMimetype()
		fileName = dm.GetFileName()
		size = dm.GetFileLength()
		thumb = dm.GetJPEGThumbnail()
		caption = dm.GetCaption()
	case m.StickerMessage != nil:
		sm := m.StickerMessage
		mediaType = "sticker"
		mime = sm.GetMimetype()
		size = sm.GetFileLength()
		w = sm.GetWidth()
		h = sm.GetHeight()
	}
	return
}

func (w *WhatsAppClient) eventHandler(evt any) {
	msg, ok := evt.(*events.Message)
	if !ok || msg.Message == nil {
		return
	}
	if isStatusJID(msg.Info.Chat) {
		return
	}

	// Mutating events (revoke / edit) come as ProtocolMessage. They reference
	// an existing stanza ID and don't have a bubble of their own.
	if pmsg := msg.Message.GetProtocolMessage(); pmsg != nil {
		switch pmsg.GetType() {
		case waE2E.ProtocolMessage_REVOKE:
			w.handleRevoke(msg, pmsg)
			return
		case waE2E.ProtocolMessage_MESSAGE_EDIT:
			w.handleEdit(msg, pmsg)
			return
		}
		// Other protocol types (key shares, ephemeral settings, app-state) are
		// internal to whatsmeow's bookkeeping; ignore.
		return
	}

	// Reactions are delivered as Message events with ReactionMessage set.
	// They reference an existing message by stanza ID — no UI bubble of their
	// own; we patch the target's persisted record and notify the UI.
	if rxn := msg.Message.GetReactionMessage(); rxn != nil {
		w.handleReaction(msg, rxn)
		return
	}

	ts := msg.Info.Timestamp
	sender := msg.Info.Sender

	text := extractText(msg.Message)
	mediaType, mime, fileName, size, mw, mh, dur, thumb, caption := extractMedia(msg.Message)
	if text == "" {
		text = caption
	}
	if text == "" && mediaType == "" {
		text = unsupportedPlaceholder(msg.Message)
	}
	if text == "" && mediaType == "" {
		return
	}
	ctx := extractContext(msg.Message)
	rid, rsenderJID, _, rtext, rmediaType := extractReply(ctx)
	rsenderName := ""
	if rsenderJID != "" {
		if jid, err := types.ParseJID(rsenderJID); err == nil {
			rsenderName = w.LookupName(jid)
		}
	}
	senderName := w.resolveDisplayName(sender, msg.Info.PushName)

	messageEvent := MessageEvent{
		Info:              msg.Info,
		Text:              text,
		SenderName:        senderName,
		SenderJid:         sender,
		Timestamp:         ts.Unix(),
		IsGroup:           msg.Info.IsGroup,
		MediaType:         mediaType,
		Mimetype:          mime,
		FileName:          fileName,
		FileSize:          size,
		Width:             mw,
		Height:            mh,
		Duration:          dur,
		Thumb:             thumb,
		Forwarded:         ctx.GetIsForwarded(),
		GifPlayback:       msg.Message.GetVideoMessage().GetGifPlayback(),
		ReplyToID:         rid,
		ReplyToSenderJID:  rsenderJID,
		ReplyToSenderName: rsenderName,
		ReplyToText:       rtext,
		ReplyToMediaType:  rmediaType,
	}

	w.seedChatRegistry(msg.Info.Chat.String(), msg.Info.IsGroup, senderName)

	w.persistIncoming(msg, messageEvent)

	if w.OnMessage != nil {
		w.OnMessage(messageEvent)
	}

	if sender.User != "" {
		w.rememberPushName(sender, msg.Info.PushName)
	}

	if mediaType != "" {
		go w.downloadAndPatch(msg)
	}
}

// handleRevoke marks the target message as deleted-for-everyone. The actual
// content stays in the JSONL (so we have a record), but UI hides it behind a
// "this message was deleted" placeholder.
func (w *WhatsAppClient) handleRevoke(msg *events.Message, pmsg *waE2E.ProtocolMessage) {
	key := pmsg.GetKey()
	targetID := key.GetID()
	chatJID := msg.Info.Chat.String()
	if targetID == "" {
		return
	}
	ts := msg.Info.Timestamp.Unix()

	w.patchRecord(chatJID, targetID, func(rec *SavedMessage) bool {
		if rec.Deleted {
			return false
		}
		rec.Deleted = true
		rec.DeletedAt = ts
		return true
	})

	if w.OnMessageDelete != nil {
		w.OnMessageDelete(MessageDelete{
			ChatJID:   chatJID,
			MessageID: targetID,
			DeletedAt: ts,
		})
	}
}

// handleEdit updates the target message's text in place. The new content is
// in pmsg.EditedMessage (a Message proto). We only update the visible text;
// media and other metadata stay untouched (WhatsApp doesn't allow editing
// media — only captions and text).
func (w *WhatsAppClient) handleEdit(msg *events.Message, pmsg *waE2E.ProtocolMessage) {
	key := pmsg.GetKey()
	targetID := key.GetID()
	chatJID := msg.Info.Chat.String()
	if targetID == "" {
		return
	}
	edited := pmsg.GetEditedMessage()
	if edited == nil {
		return
	}
	newText := extractText(edited)
	if newText == "" {
		// Edit might be of a media caption — recover from extractMedia.
		_, _, _, _, _, _, _, _, caption := extractMedia(edited)
		newText = caption
	}
	ts := msg.Info.Timestamp.Unix()

	w.patchRecord(chatJID, targetID, func(rec *SavedMessage) bool {
		if rec.Text == newText && rec.Edited {
			return false
		}
		rec.Text = newText
		rec.Edited = true
		rec.EditedAt = ts
		return true
	})

	if w.OnMessageEdit != nil {
		w.OnMessageEdit(MessageEdit{
			ChatJID:   chatJID,
			MessageID: targetID,
			NewText:   newText,
			EditedAt:  ts,
		})
	}
}

// receiptStatus maps a whatsmeow ReceiptType to our normalized status string.
// We only care about the cases that make a checkmark change in the UI.
func receiptStatus(rt types.ReceiptType) string {
	switch rt {
	case types.ReceiptTypeDelivered:
		return "delivered"
	case types.ReceiptTypeRead, types.ReceiptTypeReadSelf:
		return "read"
	case types.ReceiptTypePlayed, types.ReceiptTypePlayedSelf:
		return "played"
	}
	return ""
}

// statusRank lets patchRecord refuse to downgrade ("" < delivered < read/played).
func statusRank(s string) int {
	switch s {
	case "delivered":
		return 1
	case "read":
		return 2
	case "played":
		return 2
	}
	return 0
}

// handleReceipt updates Status on every message in the receipt's batch and
// fires OnMessageStatus per message so the UI can refresh checkmarks in place.
func (w *WhatsAppClient) handleReceipt(r *events.Receipt) {
	status := receiptStatus(r.Type)
	if status == "" {
		return
	}
	chatJID := r.Chat.String()
	for _, mid := range r.MessageIDs {
		msgID := string(mid)
		updated := false
		w.patchRecord(chatJID, msgID, func(rec *SavedMessage) bool {
			if statusRank(status) <= statusRank(rec.Status) {
				return false
			}
			rec.Status = status
			updated = true
			return true
		})
		if updated && w.OnMessageStatus != nil {
			w.OnMessageStatus(MessageStatus{
				ChatJID:   chatJID,
				MessageID: msgID,
				Status:    status,
			})
		}
	}
}

// handleReaction updates the target message's Reactions list. WhatsApp's
// model: each user has ≤1 reaction per message; an empty emoji removes the
// existing one. We patch the persisted record and fire OnReactionUpdate.
//
// Covers reactions from third parties AND from the user's other devices
// (phone echo via multi-device fan-out). Reactions sent by THIS device
// never arrive here — the server doesn't echo a device's own sends back
// to it — so SendReaction applies its own local echo.
func (w *WhatsAppClient) handleReaction(msg *events.Message, rxn *waE2E.ReactionMessage) {
	key := rxn.GetKey()
	targetID := key.GetID()
	if targetID == "" {
		return
	}
	sender := msg.Info.Sender
	senderJID := sender.ToNonAD().String()
	if msg.Info.IsFromMe {
		// Canonicalize our own JID so the same account's reaction dedupes
		// whether it arrives LID- or PN-addressed (and matches the local
		// echo SendReaction already applied).
		senderJID = w.ownSenderJID()
	}
	senderName := msg.Info.PushName
	if senderName == "" {
		senderName = w.LookupName(sender)
	}

	r := SavedReaction{
		Emoji:      rxn.GetText(),
		SenderJID:  senderJID,
		SenderName: senderName,
		Timestamp:  msg.Info.Timestamp.Unix(),
	}
	if w.applyReaction(msg.Info.Chat.String(), targetID, r) {
		return
	}
	// Target not stored under this chat JID — the message may have been
	// persisted under the LID/PN sibling chat. Retry there instead of
	// firing a destructive update with an empty list.
	if sib, ok := w.siblingChatJID(msg.Info.Chat); ok {
		w.applyReaction(sib.String(), targetID, r)
	}
}

// mergeReaction drops any prior reaction from r.SenderJID and appends r when
// its emoji is non-empty (WhatsApp: at most one reaction per user per
// message; an empty emoji is a removal). Callers must canonicalize the
// sender JIDs first — comparison is exact-string. Pure helper, unit-tested.
func mergeReaction(existing []SavedReaction, r SavedReaction) []SavedReaction {
	filtered := existing[:0]
	for _, old := range existing {
		if old.SenderJID != r.SenderJID {
			filtered = append(filtered, old)
		}
	}
	if r.Emoji != "" {
		filtered = append(filtered, r)
	}
	return filtered
}

// dedupeReactions collapses entries that share a SenderJID, keeping the
// last (= most recently appended) one. Needed once for records persisted
// before sender canonicalization: the same user's LID- and PN-flavoured
// entries collide after canonSenderJID folds them. Pure helper, unit-tested.
func dedupeReactions(rs []SavedReaction) []SavedReaction {
	lastIdx := make(map[string]int, len(rs))
	for i, r := range rs {
		lastIdx[r.SenderJID] = i
	}
	out := rs[:0]
	for i, r := range rs {
		if lastIdx[r.SenderJID] == i {
			out = append(out, r)
		}
	}
	return out
}

// canonSenderJID normalizes a reaction sender for dedupe: strips the device
// (legacy records stored AD JIDs like "<user>:23@server"), folds the
// account's own LID/PN flavours into the canonical own JID, and bridges
// third-party LIDs to their phone JID when the mapping is known.
// Best-effort — unparseable input and unmapped LIDs pass through.
func (w *WhatsAppClient) canonSenderJID(s string) string {
	if s == "" {
		return s
	}
	jid, err := types.ParseJID(s)
	if err != nil {
		return s
	}
	jid = jid.ToNonAD()
	if w.client != nil && w.client.Store != nil {
		if own := w.client.Store.ID; own != nil && jid.User == own.User {
			return w.ownSenderJID()
		}
		if lid := w.client.Store.LID; !lid.IsEmpty() && jid.User == lid.User {
			return w.ownSenderJID()
		}
	}
	if jid.Server == types.HiddenUserServer {
		if pn, ok := w.lookupPNForLID(jid); ok {
			return pn.ToNonAD().String()
		}
	}
	return jid.String()
}

// applyReaction merges one user's reaction into the target message's
// persisted Reactions and notifies the UI with the resulting full list.
// Returns false (and stays silent) when no record matches (chatJID,
// targetID) — Patch is a no-op on missing rows, and notifying anyway would
// wipe the UI's in-memory chips with an empty list.
//
// Stored senders are canonicalized on the way through, which both makes the
// merge match records persisted before canonicalization existed (raw AD /
// LID-flavoured JIDs) and migrates those records in place on write-back.
func (w *WhatsAppClient) applyReaction(chatJID, targetID string, r SavedReaction) bool {
	patched := false
	var current []SavedReaction
	r.SenderJID = w.canonSenderJID(r.SenderJID)
	w.patchRecord(chatJID, targetID, func(rec *SavedMessage) bool {
		for i := range rec.Reactions {
			rec.Reactions[i].SenderJID = w.canonSenderJID(rec.Reactions[i].SenderJID)
		}
		rec.Reactions = mergeReaction(dedupeReactions(rec.Reactions), r)
		current = rec.Reactions
		patched = true
		return true
	})
	if !patched {
		return false
	}
	if w.OnReactionUpdate != nil {
		w.OnReactionUpdate(ReactionUpdate{
			ChatJID:   chatJID,
			MessageID: targetID,
			Reactions: current,
		})
	}
	return true
}

// ownSenderJID is the canonical (non-AD, PN-flavoured) JID string used to
// identify this account in reaction records, regardless of whether an event
// addressed us by LID or phone number.
func (w *WhatsAppClient) ownSenderJID() string {
	own := w.OwnJID()
	if own.IsEmpty() {
		return ""
	}
	return own.ToNonAD().String()
}

// siblingChatJID maps a 1-1 chat JID to its LID/PN twin, when whatsmeow's
// lid_map knows the pairing. Group and unmapped JIDs return ok=false.
func (w *WhatsAppClient) siblingChatJID(chat types.JID) (types.JID, bool) {
	switch chat.Server {
	case types.HiddenUserServer:
		return w.lookupPNForLID(chat)
	case types.DefaultUserServer:
		if w.client == nil || w.client.Store == nil || w.client.Store.LIDs == nil {
			return types.JID{}, false
		}
		lid, err := w.client.Store.LIDs.GetLIDForPN(context.Background(), chat)
		if err != nil || lid.IsEmpty() {
			return types.JID{}, false
		}
		return lid, true
	}
	return types.JID{}, false
}

// persistIncoming writes a SavedMessage record for a freshly-arrived event.
// MediaPath stays empty until downloadAndPatch fills it asynchronously.
func (w *WhatsAppClient) persistIncoming(msg *events.Message, evt MessageEvent) {
	if evt.Text == "" && evt.MediaType == "" {
		return
	}
	rec := SavedMessage{
		ID:                msg.Info.ID,
		ChatJID:           msg.Info.Chat.String(),
		SenderJID:         msg.Info.Sender.String(),
		SenderName:        evt.SenderName,
		Text:              evt.Text,
		Timestamp:         msg.Info.Timestamp.Unix(),
		FromMe:            msg.Info.IsFromMe,
		MediaType:         evt.MediaType,
		Mimetype:          evt.Mimetype,
		FileName:          evt.FileName,
		FileSize:          evt.FileSize,
		Width:             evt.Width,
		Height:            evt.Height,
		Duration:          evt.Duration,
		Forwarded:         evt.Forwarded,
		GifPlayback:       evt.GifPlayback,
		ReplyToID:         evt.ReplyToID,
		ReplyToSenderJID:  evt.ReplyToSenderJID,
		ReplyToSenderName: evt.ReplyToSenderName,
		ReplyToText:       evt.ReplyToText,
		ReplyToMediaType:  evt.ReplyToMediaType,
	}
	if len(evt.Thumb) > 0 {
		rec.ThumbB64 = base64.StdEncoding.EncodeToString(evt.Thumb)
	}
	if err := w.msgStore.InsertBatch([]SavedMessage{rec}); err != nil {
		log.Printf("persistIncoming: %v", err)
	}
}

// isStatusJID returns true for the status@broadcast pseudo-chat that holds
// 24h stories — feature we don't surface in this app.
func isStatusJID(jid types.JID) bool {
	return jid.Server == types.BroadcastServer && jid.User == types.StatusBroadcastJID.User
}

// extractText pulls the user-visible text from a whatsmeow Message proto.
func extractText(m *waE2E.Message) string {
	if m == nil {
		return ""
	}
	if t := m.GetConversation(); t != "" {
		return t
	}
	if t := m.GetExtendedTextMessage().GetText(); t != "" {
		return t
	}
	return ""
}

// unsupportedPlaceholder returns a human-readable placeholder for message
// types the client doesn't fully render (polls, locations, contacts, etc.).
// Returns "" when the message is a type we DO handle (text, image, video,
// audio, document, sticker) or when it's nil/unrecognized.
func unsupportedPlaceholder(m *waE2E.Message) string {
	if m == nil {
		return ""
	}
	switch {
	case m.PollCreationMessage != nil:
		q := m.PollCreationMessage.GetName()
		if q != "" {
			return "\U0001F4CA " + q
		}
		return "\U0001F4CA Poll"
	case m.LocationMessage != nil:
		return "\U0001F4CD Location"
	case m.LiveLocationMessage != nil:
		return "\U0001F4CD Live location"
	case m.ContactMessage != nil:
		name := m.ContactMessage.GetDisplayName()
		if name != "" {
			return "\U0001F464 " + name
		}
		return "\U0001F464 Contact"
	case m.ContactsArrayMessage != nil:
		return "\U0001F465 Contacts"
	case m.EventMessage != nil:
		return "\U0001F4C5 Event"
	case m.GroupInviteMessage != nil:
		return "\U0001F517 Group invite"
	case m.ViewOnceMessage != nil:
		return "\U0001F441 View once"
	case m.PtvMessage != nil:
		return "\U0001F3A5 Video message"
	case m.OrderMessage != nil:
		return "\U0001F6D2 Order"
	case m.PollUpdateMessage != nil:
		return ""
	}
	return ""
}

// handleHistorySync processes a batch of historical messages from the phone,
// dedupes against what's already on disk, and persists the rest.
func (w *WhatsAppClient) handleHistorySync(evt *events.HistorySync) {
	if evt == nil || evt.Data == nil {
		return
	}

	convs := evt.Data.GetConversations()
	if len(convs) == 0 {
		return
	}

	totalNew := 0
	for _, conv := range convs {
		jidStr := conv.GetID()
		if jidStr == "" {
			continue
		}
		jid, err := types.ParseJID(jidStr)
		if err != nil {
			continue
		}
		if isStatusJID(jid) {
			continue
		}

		// Make groups appear in the sidebar even before FetchGroups completes.
		if jid.Server == types.GroupServer {
			w.muGroups.Lock()
			if _, ok := w.groupCache[jidStr]; !ok {
				w.groupCache[jidStr] = jid.User
			}
			w.muGroups.Unlock()
		}

		hmsgs := conv.GetMessages()
		if len(hmsgs) == 0 {
			continue
		}

		var batch []SavedMessage

		for _, hm := range hmsgs {
			wmsg := hm.GetMessage()
			if wmsg == nil {
				continue
			}
			body := wmsg.GetMessage()
			// Skip reaction-only messages — those reference an earlier message
			// by ID, and HistorySync may deliver them out of order. We let the
			// real-time handleReaction path cover live reactions instead.
			if body.GetReactionMessage() != nil {
				continue
			}
			text := extractText(body)
			mediaType, mime, fileName, size, mw, mh, dur, thumb, caption := extractMedia(body)
			if text == "" {
				text = caption
			}
			if text == "" && mediaType == "" {
				text = unsupportedPlaceholder(body)
			}
			if text == "" && mediaType == "" {
				continue
			}
			key := wmsg.GetKey()
			id := key.GetID()
			sender := key.GetParticipant()
			if sender == "" {
				if key.GetFromMe() && w.client != nil && w.client.Store.ID != nil {
					sender = w.client.Store.ID.String()
				} else {
					sender = jidStr
				}
			}
			pushName := wmsg.GetPushName()
			senderName := pushName
			if senderJID, err := types.ParseJID(sender); err == nil {
				senderName = w.resolveDisplayName(senderJID, pushName)
			}

			bctx := extractContext(body)
			rid, rsenderJID, _, rtext, rmediaType := extractReply(bctx)
			rsenderName := ""
			if rsenderJID != "" {
				if rsj, err := types.ParseJID(rsenderJID); err == nil {
					rsenderName = w.LookupName(rsj)
				}
			}

			rec := SavedMessage{
				ID:                id,
				ChatJID:           jidStr,
				SenderJID:         sender,
				SenderName:        senderName,
				Text:              text,
				Timestamp:         int64(wmsg.GetMessageTimestamp()),
				FromMe:            key.GetFromMe(),
				MediaType:         mediaType,
				Mimetype:          mime,
				FileName:          fileName,
				FileSize:          size,
				Width:             mw,
				Height:            mh,
				Duration:          dur,
				Forwarded:         bctx.GetIsForwarded(),
				GifPlayback:       body.GetVideoMessage().GetGifPlayback(),
				ReplyToID:         rid,
				ReplyToSenderJID:  rsenderJID,
				ReplyToSenderName: rsenderName,
				ReplyToText:       rtext,
				ReplyToMediaType:  rmediaType,
			}
			if len(thumb) > 0 {
				rec.ThumbB64 = base64.StdEncoding.EncodeToString(thumb)
			}
			batch = append(batch, rec)
		}

		if len(batch) == 0 {
			continue
		}
		// HistorySync delivers newest-first; persist in chronological order
		// so file appends remain time-ordered overall.
		sort.Slice(batch, func(i, j int) bool {
			return batch[i].Timestamp < batch[j].Timestamp
		})
		if err := w.msgStore.InsertBatch(batch); err != nil {
			log.Printf("HistorySync: persist %d messages for %s: %v", len(batch), jidStr, err)
			continue
		}
		totalNew += len(batch)
	}

	if totalNew > 0 {
		log.Printf("HistorySync: imported %d new messages across %d chats", totalNew, len(convs))
		if w.OnHistoryUpdate != nil {
			w.OnHistoryUpdate()
		}
	}
}

// ReplyTo describes the message a new outgoing message is quoting.
// Used by SendMessage when reply != nil. SenderJID is best-effort —
// WhatsApp will still render the quote without it for 1-1 chats, but
// groups expect Participant. SenderName is the display name persisted
// alongside the record so the quote header survives a restart.
type ReplyTo struct {
	MessageID  string
	SenderJID  string
	SenderName string
	QuotedText string
}

// GenerateMessageID returns a fresh, valid WhatsApp message ID. Used for
// optimistic UI sends: the caller stamps the bubble with this ID *before*
// firing the network call, so the user sees their message land instantly
// instead of waiting on the round-trip ACK. Empty if not connected.
func (w *WhatsAppClient) GenerateMessageID() string {
	if w.client == nil {
		return ""
	}
	return string(w.client.GenerateMessageID())
}

// SendMessage sends a text message to a chat. If reply is non-nil, it is
// sent as a reply to the referenced message (waE2E.ExtendedTextMessage
// with ContextInfo.QuotedMessage). If id is non-empty, the server is
// asked to use that ID — used by the UI's optimistic-send path so the
// bubble it added pre-ACK matches the eventual server record. On success
// it persists the outgoing record locally so it survives a restart even
// if whatsmeow's own-device echo never fires. Returns the message ID.
func (w *WhatsAppClient) SendMessage(jid types.JID, text string, reply *ReplyTo, id string) (string, error) {
	if !w.IsConnected() {
		return "", fmt.Errorf("not connected to WhatsApp")
	}

	var msg *waE2E.Message
	if reply == nil {
		msg = &waE2E.Message{Conversation: proto.String(text)}
	} else {
		ctx := &waE2E.ContextInfo{
			StanzaID:      proto.String(reply.MessageID),
			QuotedMessage: &waE2E.Message{Conversation: proto.String(reply.QuotedText)},
		}
		if reply.SenderJID != "" {
			ctx.Participant = proto.String(reply.SenderJID)
		}
		msg = &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text:        proto.String(text),
				ContextInfo: ctx,
			},
		}
	}

	var resp whatsmeow.SendResponse
	var err error
	if id != "" {
		resp, err = w.client.SendMessage(context.Background(), jid, msg,
			whatsmeow.SendRequestExtra{ID: types.MessageID(id)})
	} else {
		resp, err = w.client.SendMessage(context.Background(), jid, msg)
	}
	if err != nil {
		return "", err
	}

	saved := SavedMessage{
		ID:        resp.ID,
		ChatJID:   jid.String(),
		Text:      text,
		Timestamp: resp.Timestamp.Unix(),
		FromMe:    true,
	}
	if reply != nil {
		saved.ReplyToID = reply.MessageID
		saved.ReplyToSenderJID = reply.SenderJID
		saved.ReplyToSenderName = reply.SenderName
		saved.ReplyToText = reply.QuotedText
	}
	w.persistOwn(saved)
	return resp.ID, nil
}

// MarkTarget identifies one incoming message to acknowledge with a read
// receipt. SenderJID matters in groups — WhatsApp requires receipts grouped
// per message author.
type MarkTarget struct {
	ID        string
	SenderJID string
}

// groupMarkTargets clusters targets by sender JID, preserving first-seen
// order so receipt batches stay deterministic. Targets without an ID are
// dropped. Pure helper, unit-tested.
func groupMarkTargets(targets []MarkTarget) ([]string, map[string][]types.MessageID) {
	order := make([]string, 0, 2)
	bySender := make(map[string][]types.MessageID, 2)
	for _, t := range targets {
		if t.ID == "" {
			continue
		}
		if _, ok := bySender[t.SenderJID]; !ok {
			order = append(order, t.SenderJID)
		}
		bySender[t.SenderJID] = append(bySender[t.SenderJID], types.MessageID(t.ID))
	}
	return order, bySender
}

// MarkRead sends read receipts for incoming messages of chatJID — one
// whatsmeow call per distinct sender (group-chat requirement). The account's
// read-receipts privacy setting is honored by the library: with receipts
// disabled the node downgrades to "read-self", which still clears the
// phone's unread badge without blue-ticking the sender.
func (w *WhatsAppClient) MarkRead(chatJID string, targets []MarkTarget) error {
	if w.client == nil || !w.IsConnected() {
		return fmt.Errorf("not connected to WhatsApp")
	}
	chat, err := types.ParseJID(chatJID)
	if err != nil {
		return fmt.Errorf("parse chat jid %q: %w", chatJID, err)
	}
	order, bySender := groupMarkTargets(targets)
	now := time.Now()
	for _, senderStr := range order {
		sender, err := types.ParseJID(senderStr)
		if err != nil {
			log.Printf("MarkRead: skip invalid sender %q: %v", senderStr, err)
			continue
		}
		ids := bySender[senderStr]
		if err := w.client.MarkRead(context.Background(), ids, now, chat, sender); err != nil {
			return fmt.Errorf("mark %d msg(s) read in %s: %w", len(ids), chatJID, err)
		}
	}
	return nil
}

// OwnJID returns the JID of the device this client is logged in as, or the
// zero JID if not logged in. Used when constructing reply context where the
// quoted message is one of our own.
func (w *WhatsAppClient) OwnJID() types.JID {
	if w.client == nil || w.client.Store == nil || w.client.Store.ID == nil {
		return types.JID{}
	}
	return *w.client.Store.ID
}

// SendReaction emits an emoji reaction to the referenced message. Empty
// emoji removes the user's previous reaction. WhatsApp accepts at most one
// reaction per user per message — sending a different emoji replaces the
// previous one server-side.
//
// The server never echoes this device's own sends back to it, so after a
// successful send we apply the reaction locally (persist + OnReactionUpdate)
// — without this the UI would never show the user's own reactions.
func (w *WhatsAppClient) SendReaction(chat, sender types.JID, msgID, emoji string) error {
	if !w.IsConnected() {
		return fmt.Errorf("not connected to WhatsApp")
	}
	msg := w.client.BuildReaction(chat, sender, types.MessageID(msgID), emoji)
	resp, err := w.client.SendMessage(context.Background(), chat, msg)
	if err != nil {
		return err
	}

	r := SavedReaction{
		Emoji:     emoji,
		SenderJID: w.ownSenderJID(),
		Timestamp: resp.Timestamp.Unix(),
	}
	if !w.applyReaction(chat.String(), msgID, r) {
		// The target may be persisted under the LID/PN sibling chat.
		if sib, ok := w.siblingChatJID(chat); ok {
			w.applyReaction(sib.String(), msgID, r)
		}
	}
	return nil
}

// persistOwn writes a freshly-sent record to the chat's JSONL. Tolerates
// being called for the same ID twice (loadMessagesFromDisk dedupes by ID),
// but normal flow only invokes once per send.
func (w *WhatsAppClient) persistOwn(rec SavedMessage) error {
	if rec.ChatJID == "" {
		return nil
	}
	if rec.Text == "" && rec.MediaType == "" {
		return nil
	}
	if rec.FromMe && rec.SenderJID == "" {
		// Stamp our own JID so later actions on this record (reacting,
		// quoting) don't have to guess the sender.
		rec.SenderJID = w.ownSenderJID()
	}
	if rec.Timestamp == 0 {
		rec.Timestamp = time.Now().Unix()
	}
	if err := w.msgStore.InsertBatch([]SavedMessage{rec}); err != nil {
		log.Printf("persistOwn: %v", err)
		return err
	}
	return nil
}

// finishOutgoing persists an outgoing record and returns it for optimistic
// rendering. A persistence failure is logged but NOT fatal: the message was
// already sent remotely, and the caller renders the bubble from the returned
// record rather than a DB read-back, so a local DB hiccup can't make a sent
// message silently vanish from the UI.
func (w *WhatsAppClient) finishOutgoing(rec SavedMessage) (SavedMessage, error) {
	if err := w.persistOwn(rec); err != nil {
		log.Printf("finishOutgoing: persist %s/%s failed (message was sent): %v", rec.ChatJID, rec.ID, err)
	}
	return rec, nil
}

// stashOutgoingMedia copies the source file to media/<chat>/<msg>.<ext>
// so the persisted record points at a stable location (the user's source
// path may move/disappear). Returns the new path or "" on failure.
func stashOutgoingMedia(srcPath, chatJID, msgID, mime string) string {
	if srcPath == "" || msgID == "" {
		return ""
	}
	ext := extForMime(mime)
	if ext == ".bin" {
		// Fall back to the source's actual extension when the mime is
		// generic/octet-stream — keeps "report.pdf" naming on disk.
		if i := strings.LastIndex(srcPath, "."); i >= 0 {
			ext = srcPath[i:]
		}
	}
	target := mediaPath(chatJID, msgID, ext)
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return ""
	}
	in, err := os.Open(srcPath)
	if err != nil {
		return ""
	}
	defer in.Close()
	out, err := os.Create(target)
	if err != nil {
		return ""
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return ""
	}
	return target
}

// SendImage sends an image message with a JPEG thumbnail and correct dimensions
// so recipients see a proper preview before downloading.
// Persists the outgoing record locally + stashes a copy of the file under
// media/ so the chat history survives a restart.
func (w *WhatsAppClient) SendImage(jid types.JID, path string, caption string) (SavedMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SavedMessage{}, fmt.Errorf("read image: %w", err)
	}

	resp, err := w.client.Upload(context.Background(), data, whatsmeow.MediaImage)
	if err != nil {
		return SavedMessage{}, fmt.Errorf("upload image: %w", err)
	}

	mime := http.DetectContentType(data)

	width, height, thumb := decodeAndThumbnail(data)

	imageMsg := &waE2E.ImageMessage{
		Caption:       proto.String(caption),
		Mimetype:      proto.String(mime),
		URL:           &resp.URL,
		DirectPath:    &resp.DirectPath,
		MediaKey:      resp.MediaKey,
		FileEncSHA256: resp.FileEncSHA256,
		FileSHA256:    resp.FileSHA256,
		FileLength:    proto.Uint64(uint64(len(data))),
	}
	if width > 0 && height > 0 {
		imageMsg.Width = proto.Uint32(uint32(width))
		imageMsg.Height = proto.Uint32(uint32(height))
	}
	if len(thumb) > 0 {
		imageMsg.JPEGThumbnail = thumb
	}

	sendResp, err := w.client.SendMessage(context.Background(), jid, &waE2E.Message{
		ImageMessage: imageMsg,
	})
	if err != nil {
		return SavedMessage{}, err
	}

	rec := SavedMessage{
		ID:        sendResp.ID,
		ChatJID:   jid.String(),
		Text:      caption,
		Timestamp: sendResp.Timestamp.Unix(),
		FromMe:    true,
		MediaType: "image",
		MediaPath: stashOutgoingMedia(path, jid.String(), sendResp.ID, mime),
		Mimetype:  mime,
		FileSize:  uint64(len(data)),
		Width:     uint32(width),
		Height:    uint32(height),
	}
	if len(thumb) > 0 {
		rec.ThumbB64 = base64.StdEncoding.EncodeToString(thumb)
	}
	return w.finishOutgoing(rec)
}

// SendFile sends a file message
func (w *WhatsAppClient) SendFile(jid types.JID, path string, filename string) (SavedMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SavedMessage{}, fmt.Errorf("failed to read file: %w", err)
	}

	mime := http.DetectContentType(data)
	resp, err := w.client.Upload(context.Background(), data, whatsmeow.MediaDocument)
	if err != nil {
		return SavedMessage{}, fmt.Errorf("failed to upload file: %w", err)
	}

	sendResp, err := w.client.SendMessage(context.Background(), jid, &waE2E.Message{
		DocumentMessage: &waE2E.DocumentMessage{
			URL:           &resp.URL,
			DirectPath:    &resp.DirectPath,
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			Mimetype:      proto.String(mime),
			FileName:      proto.String(filename),
		},
	})
	if err != nil {
		return SavedMessage{}, err
	}

	return w.finishOutgoing(SavedMessage{
		ID:        sendResp.ID,
		ChatJID:   jid.String(),
		Timestamp: sendResp.Timestamp.Unix(),
		FromMe:    true,
		MediaType: "document",
		MediaPath: stashOutgoingMedia(path, jid.String(), sendResp.ID, mime),
		Mimetype:  mime,
		FileName:  filename,
		FileSize:  uint64(len(data)),
	})
}

// SendAudio sends an audio/voice message (OPUS format)
func (w *WhatsAppClient) SendAudio(jid types.JID, path string) (SavedMessage, error) {
	return w.sendAudio(jid, path, false)
}

// SendVoice sends a push-to-talk voice note.
func (w *WhatsAppClient) SendVoice(jid types.JID, path string) (SavedMessage, error) {
	return w.sendAudio(jid, path, true)
}

func (w *WhatsAppClient) sendAudio(jid types.JID, path string, voice bool) (SavedMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SavedMessage{}, fmt.Errorf("failed to read audio file: %w", err)
	}

	resp, err := w.client.Upload(context.Background(), data, whatsmeow.MediaAudio)
	if err != nil {
		return SavedMessage{}, fmt.Errorf("failed to upload audio: %w", err)
	}

	mime := "audio/ogg; codecs=opus"
	audioMsg := &waE2E.AudioMessage{
		URL:           &resp.URL,
		DirectPath:    &resp.DirectPath,
		MediaKey:      resp.MediaKey,
		FileEncSHA256: resp.FileEncSHA256,
		FileSHA256:    resp.FileSHA256,
		FileLength:    proto.Uint64(uint64(len(data))),
		Mimetype:      proto.String(mime),
	}
	mediaType := "audio"
	if voice {
		audioMsg.PTT = proto.Bool(true)
		mediaType = "voice"
	}
	sendResp, err := w.client.SendMessage(context.Background(), jid, &waE2E.Message{
		AudioMessage: audioMsg,
	})
	if err != nil {
		return SavedMessage{}, err
	}

	return w.finishOutgoing(SavedMessage{
		ID:        sendResp.ID,
		ChatJID:   jid.String(),
		Timestamp: sendResp.Timestamp.Unix(),
		FromMe:    true,
		MediaType: mediaType,
		MediaPath: stashOutgoingMedia(path, jid.String(), sendResp.ID, mime),
		Mimetype:  mime,
		FileSize:  uint64(len(data)),
	})
}

// GetChats returns the list of active chats from memory cache
func (w *WhatsAppClient) GetChats() ([]Chat, error) {
	type chatCandidate struct {
		jid      types.JID
		fallback string
		isGroup  bool
	}

	var candidates []chatCandidate
	seen := make(map[string]bool)

	// Priority 1: chats with messages in this session
	w.muChats.RLock()
	for jidStr, name := range w.chatRegistry {
		if seen[jidStr] {
			continue
		}
		jid, err := types.ParseJID(jidStr)
		if err != nil {
			continue
		}
		if isStatusJID(jid) {
			continue
		}
		seen[jidStr] = true

		candidates = append(candidates, chatCandidate{
			jid:      jid,
			fallback: name,
			isGroup:  jid.Server == types.GroupServer,
		})
	}
	w.muChats.RUnlock()

	// Priority 2: joined groups (so groups show up even without a recent message)
	w.muGroups.RLock()
	for jidStr, name := range w.groupCache {
		if seen[jidStr] {
			continue
		}
		jid, err := types.ParseJID(jidStr)
		if err != nil {
			continue
		}
		seen[jidStr] = true
		candidates = append(candidates, chatCandidate{
			jid:      jid,
			fallback: name,
			isGroup:  true,
		})
	}
	w.muGroups.RUnlock()

	// Priority 3: contact cache
	w.muContacts.RLock()
	for jidStr, contact := range w.ContactCache {
		if seen[jidStr] {
			continue
		}
		jid, err := types.ParseJID(jidStr)
		if err != nil {
			continue
		}
		seen[jidStr] = true

		candidates = append(candidates, chatCandidate{
			jid:      jid,
			fallback: contact.Name,
			isGroup:  jid.Server == types.GroupServer,
		})
	}
	w.muContacts.RUnlock()

	chats := make([]Chat, 0, len(candidates))
	for _, candidate := range candidates {
		displayName := candidate.fallback
		if resolved := w.LookupName(candidate.jid); resolved != "" {
			displayName = resolved
		}
		chats = append(chats, Chat{
			JID:         candidate.jid,
			DisplayName: displayName,
			IsGroup:     candidate.isGroup,
		})
	}
	return chats, nil
}

// savedName returns the contact's saved address-book name (or "" if none),
// bridging @lid → phone-number JID so a LID inherits the PN contact's name.
func (w *WhatsAppClient) savedName(jid types.JID) string {
	w.muContacts.RLock()
	if c, ok := w.ContactCache[jid.String()]; ok && c.Name != "" {
		w.muContacts.RUnlock()
		return c.Name
	}
	w.muContacts.RUnlock()

	if jid.Server == types.HiddenUserServer {
		if pn, ok := w.lookupPNForLID(jid); ok {
			w.muContacts.RLock()
			if c, ok := w.ContactCache[pn.String()]; ok && c.Name != "" {
				w.muContacts.RUnlock()
				return c.Name
			}
			w.muContacts.RUnlock()
		}
	}
	return ""
}

// cachedPushName returns a learned push/business name (or "" if none),
// bridging @lid → phone-number JID like savedName.
func (w *WhatsAppClient) cachedPushName(jid types.JID) string {
	w.muContacts.RLock()
	if n, ok := w.pushNameCache[jid.String()]; ok && n != "" {
		w.muContacts.RUnlock()
		return n
	}
	w.muContacts.RUnlock()

	if jid.Server == types.HiddenUserServer {
		if pn, ok := w.lookupPNForLID(jid); ok {
			w.muContacts.RLock()
			if n, ok := w.pushNameCache[pn.String()]; ok && n != "" {
				w.muContacts.RUnlock()
				return n
			}
			w.muContacts.RUnlock()
		}
	}
	return ""
}

// resolveDisplayName is the single source of truth for turning a JID into a
// display name. Precedence for a person/LID JID:
//
//	saved address-book name  >  live push name  >  cached push/business name
//	>  chat-registry fallback  >  resolved phone  >  raw JID user
//
// Push ranks ABOVE the chat-registry fallback on purpose: a fresh push name
// beats a stale auto-seeded registry entry, and the sender bubble (called with
// the live pushName) and LookupName (called with "") converge once the push
// name is cached — which is what fixes the old two-resolvers inconsistency.
func (w *WhatsAppClient) resolveDisplayName(jid types.JID, pushName string) string {
	// All caches key by device-less JIDs; events can carry AD JIDs
	// ("<user>:<device>@server"), which would silently miss every map.
	jid = jid.ToNonAD()
	jidStr := jid.String()

	if jid.Server == types.GroupServer {
		w.muGroups.RLock()
		if name, ok := w.groupCache[jidStr]; ok && name != "" {
			w.muGroups.RUnlock()
			return name
		}
		w.muGroups.RUnlock()
		w.muChats.RLock()
		if name, ok := w.chatRegistry[jidStr]; ok && name != "" {
			w.muChats.RUnlock()
			return name
		}
		w.muChats.RUnlock()
		return jid.User
	}

	if name := w.savedName(jid); name != "" {
		return name
	}
	if strings.TrimSpace(pushName) != "" {
		return pushName
	}
	if name := w.cachedPushName(jid); name != "" {
		return name
	}

	w.muChats.RLock()
	if name, ok := w.chatRegistry[jidStr]; ok && name != "" {
		w.muChats.RUnlock()
		return name
	}
	w.muChats.RUnlock()

	if jid.Server == types.HiddenUserServer {
		if pn, ok := w.lookupPNForLID(jid); ok && pn.User != "" {
			return pn.User
		}
	}
	if jid.User != "" {
		return jid.User
	}
	return jidStr
}

// rememberPushName records a network-provided push name. It always updates
// pushNameCache (so renames surface); the resolver ranks any saved name above
// it, so this never clobbers an address-book name — and writing to a separate
// cache means FetchContacts rebuilding ContactCache can't wipe it.
func (w *WhatsAppClient) rememberPushName(jid types.JID, pushName string) {
	if jid.User == "" || strings.TrimSpace(pushName) == "" {
		return
	}
	w.muContacts.Lock()
	w.pushNameCache[jid.ToNonAD().String()] = pushName
	w.muContacts.Unlock()
}

// rememberBusinessName records a business display name. Shares pushNameCache
// with push names (last writer wins) — both are network-provided names that
// rank below a saved contact name.
func (w *WhatsAppClient) rememberBusinessName(jid types.JID, businessName string) {
	if jid.User == "" || strings.TrimSpace(businessName) == "" {
		return
	}
	w.muContacts.Lock()
	w.pushNameCache[jid.ToNonAD().String()] = businessName
	w.muContacts.Unlock()
}

// seedChatRegistry records a fallback display name for a newly-seen 1:1 chat.
// Groups are skipped on purpose: a group's name comes from groupCache, and
// seeding a participant's name here would mislabel the whole group in the
// sidebar until groupCache fills.
func (w *WhatsAppClient) seedChatRegistry(chatJID string, isGroup bool, name string) {
	if isGroup || name == "" {
		return
	}
	w.muChats.Lock()
	if _, exists := w.chatRegistry[chatJID]; !exists {
		w.chatRegistry[chatJID] = name
	}
	w.muChats.Unlock()
}

// LookupName returns a friendly display name for a JID using the in-memory
// caches, falling back to the JID's user component. It delegates to
// resolveDisplayName with no live push name; see that function for the full
// precedence and @lid → phone-number resolution rules.
func (w *WhatsAppClient) LookupName(jid types.JID) string {
	return w.resolveDisplayName(jid, "")
}

// lookupPNForLID resolves a LID JID to its underlying phone-number JID.
// Hits the in-memory cache first; on miss, queries whatsmeow's lid_map
// store and memoizes the answer. Returns ok=false when the mapping is
// genuinely unknown (whatsmeow learns mappings over time via history
// sync and contact-resolution events).
func (w *WhatsAppClient) lookupPNForLID(lid types.JID) (types.JID, bool) {
	w.muLIDs.RLock()
	if pn, ok := w.lidPNCache[lid.String()]; ok {
		w.muLIDs.RUnlock()
		return pn, !pn.IsEmpty()
	}
	w.muLIDs.RUnlock()

	if w.client == nil || w.client.Store == nil || w.client.Store.LIDs == nil {
		return types.JID{}, false
	}
	pn, err := w.client.Store.LIDs.GetPNForLID(context.Background(), lid)
	if err != nil {
		// Transient DB error: don't cache, let the next call retry.
		return types.JID{}, false
	}
	// Cache misses too (as the empty JID — the read path above treats it as
	// "known unknown"): quote re-resolution runs on every bubble rebuild, and
	// without a negative entry each render of an unmapped LID would hit
	// SQLite again. dropNegativeLIDEntries re-opens these when fresh contact
	// data lands.
	w.muLIDs.Lock()
	w.lidPNCache[lid.String()] = pn
	w.muLIDs.Unlock()
	return pn, !pn.IsEmpty()
}

// dropNegativeLIDEntries forgets cached "mapping unknown" answers so the
// next lookup re-queries whatsmeow's lid_map. Called when fresh contact
// data lands (FetchContacts), which is also when new LID↔PN mappings tend
// to have arrived.
func (w *WhatsAppClient) dropNegativeLIDEntries() {
	w.muLIDs.Lock()
	for k, v := range w.lidPNCache {
		if v.IsEmpty() {
			delete(w.lidPNCache, k)
		}
	}
	w.muLIDs.Unlock()
}

// PhoneForJID returns the real phone number for a chat JID, or "" if it
// can't be resolved (group, unknown LID mapping, etc.).
//
// WhatsApp now uses two JID flavours: the classic phone-number JID
// (`<phone>@s.whatsapp.net`) and the privacy-preserving LID
// (`<opaque>@lid`). Newer accounts and many group conversations use LID
// for the chat itself, so taking the JID's User part naively gives a
// meaningless opaque ID. For LID JIDs we ask whatsmeow's LID store for
// the mapped phone-number JID; the mapping is populated over time
// (history sync, contact lookups), so it can legitimately come back empty.
func (w *WhatsAppClient) PhoneForJID(jid types.JID) string {
	switch jid.Server {
	case types.DefaultUserServer, types.LegacyUserServer, types.HostedServer:
		return jid.User
	case types.HiddenUserServer:
		if w.client == nil || w.client.Store == nil || w.client.Store.LIDs == nil {
			return ""
		}
		pn, err := w.client.Store.LIDs.GetPNForLID(context.Background(), jid)
		if err != nil || pn.IsEmpty() {
			return ""
		}
		return pn.User
	}
	return ""
}

// ChatSettingsInfo mirrors the phone-synced flags for one chat (read from
// whatsmeow's chat_settings store, populated via history/app-state sync).
type ChatSettingsInfo struct {
	Archived   bool
	Pinned     bool
	MutedUntil time.Time
}

// MutedAt reports whether the chat is muted at the given instant. whatsmeow
// stores the zero time for unmuted chats and a (possibly far-future)
// timestamp for muted ones, so a plain Before covers every case, including
// "muted forever" and expired mutes.
func (s ChatSettingsInfo) MutedAt(now time.Time) bool {
	return now.Before(s.MutedUntil)
}

// ChatSettings returns the archive/pin/mute flags for a chat, memoized until
// an app-state event invalidates the entry. Zero value on any error or
// unknown chat — callers err toward visible/unmuted, matching the previous
// IsChatArchived behavior.
func (w *WhatsAppClient) ChatSettings(jid types.JID) ChatSettingsInfo {
	key := jid.String()
	w.muChatSettings.RLock()
	if s, ok := w.chatSettingsCache[key]; ok {
		w.muChatSettings.RUnlock()
		return s
	}
	w.muChatSettings.RUnlock()

	if w.client == nil || w.client.Store == nil || w.client.Store.ChatSettings == nil {
		return ChatSettingsInfo{}
	}
	settings, err := w.client.Store.ChatSettings.GetChatSettings(context.Background(), jid)
	if err != nil {
		// Not cached: transient store errors shouldn't pin a zero value.
		return ChatSettingsInfo{}
	}
	info := ChatSettingsInfo{
		Archived:   settings.Archived,
		Pinned:     settings.Pinned,
		MutedUntil: settings.MutedUntil,
	}
	w.muChatSettings.Lock()
	w.chatSettingsCache[key] = info
	w.muChatSettings.Unlock()
	return info
}

// invalidateChatSettings drops one chat's cached flags (the store was
// already updated by whatsmeow's app-state processor before the event
// fired) and nudges the UI to repaint.
func (w *WhatsAppClient) invalidateChatSettings(jid types.JID) {
	w.muChatSettings.Lock()
	delete(w.chatSettingsCache, jid.String())
	w.muChatSettings.Unlock()
	if w.OnChatSettingsChanged != nil {
		w.OnChatSettingsChanged()
	}
}

// ResolveNumber checks whether a raw phone number (digits only, with
// country code) is registered on WhatsApp and returns its canonical JID.
// The canonical JID can differ from a naive <digits>@s.whatsapp.net —
// e.g. Brazilian numbers with/without the extra 9.
func (w *WhatsAppClient) ResolveNumber(digits string) (types.JID, error) {
	if w.client == nil || !w.IsConnected() {
		return types.JID{}, fmt.Errorf("not connected to WhatsApp")
	}
	resp, err := w.client.IsOnWhatsApp(context.Background(), []string{"+" + digits})
	if err != nil {
		return types.JID{}, fmt.Errorf("check +%s: %w", digits, err)
	}
	if len(resp) == 0 || !resp[0].IsIn {
		return types.JID{}, fmt.Errorf("+%s não está no WhatsApp", digits)
	}
	return resp[0].JID, nil
}

// SetChatArchived archives/unarchives a chat via an app-state patch — the
// same mechanism the phone uses, so the change syncs to every device.
// WhatsApp auto-unpins on archive (BuildArchive bundles that mutation).
// The local cache is updated optimistically; the server's events.Archive
// echo re-invalidates it with the authoritative state.
func (w *WhatsAppClient) SetChatArchived(jid types.JID, archived bool) error {
	if w.client == nil || !w.IsConnected() {
		return fmt.Errorf("not connected to WhatsApp")
	}
	patch := appstate.BuildArchive(jid, archived, time.Time{}, nil)
	if err := w.client.SendAppState(context.Background(), patch); err != nil {
		return fmt.Errorf("send archive app state: %w", err)
	}
	cur := w.ChatSettings(jid) // pin/mute flags stay real while we mutate
	cur.Archived = archived
	if archived {
		cur.Pinned = false
	}
	w.muChatSettings.Lock()
	w.chatSettingsCache[jid.String()] = cur
	w.muChatSettings.Unlock()
	return nil
}

// FetchGroups loads all joined groups from the WhatsApp servers into the cache.
// Returns the number of groups now in the cache (0 if not connected/logged in
// yet, or on error). Caller can use this to stop a retry loop.
func (w *WhatsAppClient) FetchGroups() int {
	if w.client == nil || !w.IsConnected() || !w.IsLoggedIn() {
		return 0
	}

	groups, err := w.client.GetJoinedGroups(context.Background())
	if err != nil {
		log.Printf("FetchGroups: %v", err)
		return 0
	}

	w.muGroups.Lock()
	for _, g := range groups {
		name := g.Name
		if name == "" {
			name = g.JID.User
		}
		w.groupCache[g.JID.String()] = name
	}
	count := len(w.groupCache)
	w.muGroups.Unlock()
	return count
}

// scheduleContactRefresh debounces contact refreshes onto a background timer.
// whatsmeow dispatches events synchronously on a single goroutine and emits one
// events.Contact per contact during sync, so calling FetchContacts (a full DB
// scan) inline would stall all event delivery. Coalescing into one background
// scan after a short quiet period keeps the event loop responsive; once the
// scan finishes we nudge the UI to repaint with the freshly-loaded names.
func (w *WhatsAppClient) scheduleContactRefresh() {
	w.muContactRefresh.Lock()
	defer w.muContactRefresh.Unlock()
	if w.contactRefreshTimer != nil {
		w.contactRefreshTimer.Stop()
	}
	w.contactRefreshTimer = time.AfterFunc(300*time.Millisecond, func() {
		w.FetchContacts()
		if w.OnContactsUpdated != nil {
			w.OnContactsUpdated()
		}
	})
}

// FetchContacts loads all contacts from the database into the memory cache.
// Prefer scheduleContactRefresh from event handlers — it runs this off the
// event-dispatch goroutine and coalesces bursts.
func (w *WhatsAppClient) FetchContacts() {
	if w.store == nil || w.client == nil || w.client.Store.ID == nil {
		return
	}

	sqlStore := sqlstore.NewSQLStore(w.store, *w.client.Store.ID)
	dbContacts, err := sqlStore.GetAllContacts(context.Background())
	if err != nil {
		log.Printf("FetchContacts: GetAllContacts failed: %v", err)
		return
	}

	// Fresh contact data often arrives together with new LID↔PN mappings —
	// give previously-unresolvable LIDs another shot at the lid_map.
	w.dropNegativeLIDEntries()

	// Build the new saved-name map and any DB-known network names outside the
	// lock to keep the critical section short.
	saved := make(map[string]Contact, len(dbContacts))
	dbPush := make(map[string]string)
	named := 0
	for jid, info := range dbContacts {
		displayName := displayNameFromContactInfo(info)
		// Leave Name empty for contacts with no saved name (typical of @lid):
		// the resolver then tries LID→PN instead of treating "" as final.
		if displayName != "" {
			named++
		}
		saved[jid.String()] = Contact{JID: jid, Name: displayName, ShortName: info.FirstName}
		net := info.BusinessName
		if strings.TrimSpace(net) == "" {
			net = info.PushName
		}
		if strings.TrimSpace(net) != "" {
			dbPush[jid.String()] = net
		}
	}

	w.muContacts.Lock()
	// ContactCache holds only saved names and is owned solely here, so a
	// wholesale replace is safe and can't wipe a learned push name. Seed
	// pushNameCache from the DB only where we have no live entry yet, so a
	// fresher event-sourced push name is never clobbered.
	w.ContactCache = saved
	for k, v := range dbPush {
		if _, ok := w.pushNameCache[k]; !ok {
			w.pushNameCache[k] = v
		}
	}
	w.muContacts.Unlock()
	log.Printf("FetchContacts: loaded %d contacts (%d saved names)", len(dbContacts), named)
}

// LoadMessages returns persisted messages for a chat in chronological order.
// Direct passthrough to MessageStore.LoadChat — kept on the client surface
// so the UI doesn't need to import the store type directly.
func (w *WhatsAppClient) LoadMessages(chatJID string) ([]SavedMessage, error) {
	return w.msgStore.LoadChat(chatJID)
}

// ChatSummaries returns one ChatSummary per known chat, ordered by recency.
// Replaces the UI's previous loop of os.ReadDir + os.Stat + tail-parse-JSONL.
func (w *WhatsAppClient) ChatSummaries() ([]ChatSummary, error) {
	return w.msgStore.ChatSummaries()
}

// GetContacts returns the full contact list
func (w *WhatsAppClient) GetContacts() []Contact {
	if !w.IsConnected() || !w.IsLoggedIn() {
		return nil
	}

	w.muContacts.RLock()
	defer w.muContacts.RUnlock()

	var contacts []Contact
	for _, c := range w.ContactCache {
		contacts = append(contacts, c)
	}
	return contacts
}

// SearchChats searches chats by name or JID
func (w *WhatsAppClient) SearchChats(query string) []Chat {
	chats, err := w.GetChats()
	if err != nil {
		return nil
	}

	var results []Chat
	queryLower := strings.ToLower(query)

	for _, chat := range chats {
		name := strings.ToLower(chat.DisplayName)
		jidStr := chat.JID.String()
		if strings.Contains(name, queryLower) || strings.Contains(jidStr, queryLower) {
			results = append(results, chat)
		}
	}

	return results
}
