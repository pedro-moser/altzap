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

// Chat represents an active chat
type Chat struct {
	JID             types.JID
	DisplayName     string
	LastMessage     string
	LastMessageTime int64
	IsGroup         bool
	AvatarURL       string
}

// WhatsAppClient wraps the whatsmeow client with higher-level operations
type WhatsAppClient struct {
	client           *whatsmeow.Client
	store            *sqlstore.Container
	msgStore         *MessageStore
	OnMessage        func(MessageEvent)
	OnLogin          LoginCallback
	OnHistoryUpdate  func()
	OnMediaReady     func(chatJID, msgID, mediaPath string) // fires when an async download finishes
	OnReactionUpdate func(ReactionUpdate)                   // fires when a reaction is added/removed
	OnMessageEdit    func(MessageEdit)                      // fires when a message's text was edited
	OnMessageDelete  func(MessageDelete)                    // fires on "delete for everyone"
	OnMessageStatus  func(MessageStatus)                    // fires when delivered/read receipt arrives
	muChannels       sync.RWMutex
	ContactCache     map[string]Contact
	muContacts       sync.RWMutex
	muMessages       sync.Mutex
	messages         map[string][]MessageEvent
	chatRegistry     map[string]string // jid -> display name
	muChats          sync.RWMutex
	groupCache       map[string]string // jid -> group name
	muGroups         sync.RWMutex
}

// NewWhatsAppClient creates a new WhatsApp client instance.
// msgStore is required — chat history persistence goes through it.
func NewWhatsAppClient(clientStore *sqlstore.Container, msgStore *MessageStore) *WhatsAppClient {
	wa := &WhatsAppClient{
		msgStore:     msgStore,
		ContactCache: make(map[string]Contact),
		messages:     make(map[string][]MessageEvent),
		chatRegistry: make(map[string]string),
		groupCache:   make(map[string]string),
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
		case *events.LoggedOut:
			log.Println("Logged out")
			wa.client.Store.ID = nil
		case *events.HistorySync:
			wa.handleHistorySync(v)
		case *events.Receipt:
			wa.handleReceipt(v)
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
	rid, rsenderJID, _, rtext, rmediaType := extractReply(extractContext(msg.Message))
	rsenderName := ""
	if rsenderJID != "" {
		if jid, err := types.ParseJID(rsenderJID); err == nil {
			rsenderName = w.LookupName(jid)
		}
	}

	messageEvent := MessageEvent{
		Info:              msg.Info,
		Text:              text,
		SenderName:        msg.Info.PushName,
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
		ReplyToID:         rid,
		ReplyToSenderJID:  rsenderJID,
		ReplyToSenderName: rsenderName,
		ReplyToText:       rtext,
		ReplyToMediaType:  rmediaType,
	}

	w.muChats.Lock()
	chatJid := msg.Info.Chat.String()
	if _, exists := w.chatRegistry[chatJid]; !exists {
		pn := msg.Info.PushName
		if pn == "" {
			pn = sender.String()
		}
		w.chatRegistry[chatJid] = pn
	}
	w.muChats.Unlock()

	w.persistIncoming(msg, mediaType, mime, fileName, size, mw, mh, dur, thumb, text,
		rid, rsenderJID, rsenderName, rtext, rmediaType)

	if w.OnMessage != nil {
		w.OnMessage(messageEvent)
	}

	if sender.User != "" {
		w.muContacts.Lock()
		w.ContactCache[sender.String()] = Contact{
			JID:        sender,
			Name:       msg.Info.PushName,
			UpdateTime: ts.Unix(),
		}
		w.muContacts.Unlock()
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
// existing one. We patch the JSONL atomically and fire OnReactionUpdate.
func (w *WhatsAppClient) handleReaction(msg *events.Message, rxn *waE2E.ReactionMessage) {
	key := rxn.GetKey()
	targetID := key.GetID()
	chatJID := msg.Info.Chat.String()
	if targetID == "" {
		return
	}
	emoji := rxn.GetText()
	senderJID := msg.Info.Sender.String()
	senderName := msg.Info.PushName
	if senderName == "" {
		senderName = w.LookupName(msg.Info.Sender)
	}
	ts := msg.Info.Timestamp.Unix()

	var current []SavedReaction
	w.patchRecord(chatJID, targetID, func(rec *SavedMessage) bool {
		// Drop any prior reaction from this sender.
		filtered := rec.Reactions[:0]
		for _, r := range rec.Reactions {
			if r.SenderJID != senderJID {
				filtered = append(filtered, r)
			}
		}
		if emoji != "" {
			filtered = append(filtered, SavedReaction{
				Emoji:      emoji,
				SenderJID:  senderJID,
				SenderName: senderName,
				Timestamp:  ts,
			})
		}
		rec.Reactions = filtered
		current = filtered
		return true
	})

	if w.OnReactionUpdate != nil {
		w.OnReactionUpdate(ReactionUpdate{
			ChatJID:   chatJID,
			MessageID: targetID,
			Reactions: current,
		})
	}
}

// persistIncoming writes a SavedMessage record for a freshly-arrived event.
// MediaPath stays empty until downloadAndPatch fills it asynchronously.
func (w *WhatsAppClient) persistIncoming(msg *events.Message, mediaType, mime, fileName string, size uint64, width, height, duration uint32, thumb []byte, text, replyToID, replyToSenderJID, replyToSenderName, replyToText, replyToMediaType string) {
	if text == "" && mediaType == "" {
		return
	}
	rec := SavedMessage{
		ID:                msg.Info.ID,
		ChatJID:           msg.Info.Chat.String(),
		SenderJID:         msg.Info.Sender.String(),
		SenderName:        msg.Info.PushName,
		Text:              text,
		Timestamp:         msg.Info.Timestamp.Unix(),
		FromMe:            msg.Info.IsFromMe,
		MediaType:         mediaType,
		Mimetype:          mime,
		FileName:          fileName,
		FileSize:          size,
		Width:             width,
		Height:            height,
		Duration:          duration,
		ReplyToID:         replyToID,
		ReplyToSenderJID:  replyToSenderJID,
		ReplyToSenderName: replyToSenderName,
		ReplyToText:       replyToText,
		ReplyToMediaType:  replyToMediaType,
	}
	if len(thumb) > 0 {
		rec.ThumbB64 = base64.StdEncoding.EncodeToString(thumb)
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

			rid, rsenderJID, _, rtext, rmediaType := extractReply(extractContext(body))
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
				SenderName:        pushName,
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
		w.msgStore.InsertBatch(batch)
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
// groups expect Participant.
type ReplyTo struct {
	MessageID  string
	SenderJID  string
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
		saved.ReplyToText = reply.QuotedText
	}
	w.persistOwn(saved)
	return resp.ID, nil
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
// previous one server-side; the eventual ReactionMessage echo is what
// updates our in-memory Message.Reactions via OnReactionUpdate.
func (w *WhatsAppClient) SendReaction(chat, sender types.JID, msgID, emoji string) error {
	if !w.IsConnected() {
		return fmt.Errorf("not connected to WhatsApp")
	}
	msg := w.client.BuildReaction(chat, sender, types.MessageID(msgID), emoji)
	_, err := w.client.SendMessage(context.Background(), chat, msg)
	return err
}

// persistOwn writes a freshly-sent record to the chat's JSONL. Tolerates
// being called for the same ID twice (loadMessagesFromDisk dedupes by ID),
// but normal flow only invokes once per send.
func (w *WhatsAppClient) persistOwn(rec SavedMessage) {
	if rec.ChatJID == "" {
		return
	}
	if rec.Text == "" && rec.MediaType == "" {
		return
	}
	if rec.Timestamp == 0 {
		rec.Timestamp = time.Now().Unix()
	}
	if err := w.msgStore.InsertBatch([]SavedMessage{rec}); err != nil {
		log.Printf("persistOwn: %v", err)
	}
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
func (w *WhatsAppClient) SendImage(jid types.JID, path string, caption string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read image: %w", err)
	}

	resp, err := w.client.Upload(context.Background(), data, whatsmeow.MediaImage)
	if err != nil {
		return "", fmt.Errorf("upload image: %w", err)
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
		return "", err
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
	w.persistOwn(rec)
	return sendResp.ID, nil
}

// SendFile sends a file message
func (w *WhatsAppClient) SendFile(jid types.JID, path string, filename string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	mime := http.DetectContentType(data)
	resp, err := w.client.Upload(context.Background(), data, whatsmeow.MediaDocument)
	if err != nil {
		return "", fmt.Errorf("failed to upload file: %w", err)
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
		return "", err
	}

	w.persistOwn(SavedMessage{
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
	return sendResp.ID, nil
}

// SendAudio sends an audio/voice message (OPUS format)
func (w *WhatsAppClient) SendAudio(jid types.JID, path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read audio file: %w", err)
	}

	resp, err := w.client.Upload(context.Background(), data, whatsmeow.MediaAudio)
	if err != nil {
		return "", fmt.Errorf("failed to upload audio: %w", err)
	}

	mime := "audio/ogg; codecs=opus"
	sendResp, err := w.client.SendMessage(context.Background(), jid, &waE2E.Message{
		AudioMessage: &waE2E.AudioMessage{
			URL:           &resp.URL,
			DirectPath:    &resp.DirectPath,
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			Mimetype:      proto.String(mime),
		},
	})
	if err != nil {
		return "", err
	}

	w.persistOwn(SavedMessage{
		ID:        sendResp.ID,
		ChatJID:   jid.String(),
		Timestamp: sendResp.Timestamp.Unix(),
		FromMe:    true,
		MediaType: "audio",
		MediaPath: stashOutgoingMedia(path, jid.String(), sendResp.ID, mime),
		Mimetype:  mime,
		FileSize:  uint64(len(data)),
	})
	return sendResp.ID, nil
}

// GetChats returns the list of active chats from memory cache
func (w *WhatsAppClient) GetChats() ([]Chat, error) {
	var chats []Chat
	seen := make(map[string]bool)

	w.muContacts.RLock()
	defer w.muContacts.RUnlock()

	// Priority 1: chats with messages in this session
	w.muChats.RLock()
	for jidStr, name := range w.chatRegistry {
		if seen[jidStr] {
			continue
		}
		jid, _ := types.ParseJID(jidStr)
		if isStatusJID(jid) {
			continue
		}
		seen[jidStr] = true

		displayName := name
		if jid.Server == types.GroupServer {
			w.muGroups.RLock()
			if gn, ok := w.groupCache[jidStr]; ok && gn != "" {
				displayName = gn
			}
			w.muGroups.RUnlock()
		} else if c, ok := w.ContactCache[jidStr]; ok && c.Name != "" {
			displayName = c.Name
		}

		chats = append(chats, Chat{
			JID:         jid,
			DisplayName: displayName,
			IsGroup:     jid.Server == types.GroupServer,
		})
	}
	w.muChats.RUnlock()

	// Priority 2: joined groups (so groups show up even without a recent message)
	w.muGroups.RLock()
	for jidStr, name := range w.groupCache {
		if seen[jidStr] {
			continue
		}
		seen[jidStr] = true
		jid, _ := types.ParseJID(jidStr)
		chats = append(chats, Chat{
			JID:         jid,
			DisplayName: name,
			IsGroup:     true,
		})
	}
	w.muGroups.RUnlock()

	// Priority 3: contact cache
	for jidStr, contact := range w.ContactCache {
		if seen[jidStr] {
			continue
		}
		seen[jidStr] = true
		jid, _ := types.ParseJID(jidStr)

		chats = append(chats, Chat{
			JID:         jid,
			DisplayName: contact.Name,
			IsGroup:     jid.Server == types.GroupServer,
		})
	}

	return chats, nil
}

// LookupName returns a friendly display name for a JID using the in-memory caches.
// Falls back to the JID's user component if nothing is known.
func (w *WhatsAppClient) LookupName(jid types.JID) string {
	jidStr := jid.String()

	if jid.Server == types.GroupServer {
		w.muGroups.RLock()
		if name, ok := w.groupCache[jidStr]; ok && name != "" {
			w.muGroups.RUnlock()
			return name
		}
		w.muGroups.RUnlock()
	}

	w.muContacts.RLock()
	if c, ok := w.ContactCache[jidStr]; ok && c.Name != "" {
		w.muContacts.RUnlock()
		return c.Name
	}
	w.muContacts.RUnlock()

	w.muChats.RLock()
	if name, ok := w.chatRegistry[jidStr]; ok && name != "" {
		w.muChats.RUnlock()
		return name
	}
	w.muChats.RUnlock()

	if jid.User != "" {
		return jid.User
	}
	return jidStr
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

// FetchContacts loads all contacts from the database into the memory cache
func (w *WhatsAppClient) FetchContacts() {
	if w.store == nil || w.client == nil || w.client.Store.ID == nil {
		return
	}

	go func() {
		sqlStore := sqlstore.NewSQLStore(w.store, *w.client.Store.ID)
		dbContacts, err := sqlStore.GetAllContacts(context.Background())
		if err != nil {
			return
		}

		w.muContacts.Lock()
		for jid, info := range dbContacts {
			displayName := ""
			if info.FullName != "" {
				displayName = info.FullName
			} else if info.PushName != "" {
				displayName = info.PushName
			} else if info.BusinessName != "" {
				displayName = info.BusinessName
			} else {
				displayName = jid.User
			}

			w.ContactCache[jid.String()] = Contact{
				JID:  jid,
				Name: displayName,
			}
		}
		w.muContacts.Unlock()
	}()
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
