package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

// savedMessage is the on-disk JSON record for a chat message. New fields are
// all optional so legacy records (text-only, prefixed sender) keep decoding.
type savedMessage struct {
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
	client          *whatsmeow.Client
	store           *sqlstore.Container
	OnMessage       func(MessageEvent)
	OnLogin         LoginCallback
	OnHistoryUpdate func()
	OnMediaReady    func(chatJID, msgID, mediaPath string) // fires when an async download finishes
	muChannels      sync.RWMutex
	ContactCache    map[string]Contact
	muContacts      sync.RWMutex
	muMessages      sync.Mutex
	messages        map[string][]MessageEvent
	chatRegistry    map[string]string // jid -> display name
	muChats         sync.RWMutex
	groupCache      map[string]string // jid -> group name
	muGroups        sync.RWMutex
	muStoreFile     sync.Mutex // serialize writes to store/*.json
}

// NewWhatsAppClient creates a new WhatsApp client instance
func NewWhatsAppClient(clientStore *sqlstore.Container) *WhatsAppClient {
	wa := &WhatsAppClient{
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

	ts := msg.Info.Timestamp
	sender := msg.Info.Sender

	text := extractText(msg.Message)
	mediaType, mime, fileName, size, mw, mh, dur, thumb, caption := extractMedia(msg.Message)
	if text == "" {
		text = caption
	}

	messageEvent := MessageEvent{
		Info:       msg.Info,
		Text:       text,
		SenderName: msg.Info.PushName,
		SenderJid:  sender,
		Timestamp:  ts.Unix(),
		IsGroup:    msg.Info.IsGroup,
		MediaType:  mediaType,
		Mimetype:   mime,
		FileName:   fileName,
		FileSize:   size,
		Width:      mw,
		Height:     mh,
		Duration:   dur,
		Thumb:      thumb,
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

	w.persistIncoming(msg, mediaType, mime, fileName, size, mw, mh, dur, thumb, text)

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

// persistIncoming writes a savedMessage record for a freshly-arrived event.
// MediaPath stays empty until downloadAndPatch fills it asynchronously.
func (w *WhatsAppClient) persistIncoming(msg *events.Message, mediaType, mime, fileName string, size uint64, width, height, duration uint32, thumb []byte, text string) {
	if text == "" && mediaType == "" {
		return
	}
	rec := savedMessage{
		ID:         msg.Info.ID,
		ChatJID:    msg.Info.Chat.String(),
		SenderJID:  msg.Info.Sender.String(),
		SenderName: msg.Info.PushName,
		Text:       text,
		Timestamp:  msg.Info.Timestamp.Unix(),
		FromMe:     msg.Info.IsFromMe,
		MediaType:  mediaType,
		Mimetype:   mime,
		FileName:   fileName,
		FileSize:   size,
		Width:      width,
		Height:     height,
		Duration:   duration,
	}
	if len(thumb) > 0 {
		rec.ThumbB64 = base64.StdEncoding.EncodeToString(thumb)
	}
	if err := w.appendMessages(rec.ChatJID, []savedMessage{rec}); err != nil {
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

// loadMessageIDs returns the set of message IDs already stored for a chat,
// for dedup when HistorySync redelivers messages we've already saved.
func (w *WhatsAppClient) loadMessageIDs(jidStr string) map[string]bool {
	out := make(map[string]bool)
	path := filepath.Join(".", "store", fmt.Sprintf("msg_%s.json", jidStr))
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for dec.More() {
		var m struct {
			ID string `json:"id"`
		}
		if err := dec.Decode(&m); err != nil {
			continue
		}
		if m.ID != "" {
			out[m.ID] = true
		}
	}
	return out
}

// appendMessages writes records to store/msg_<jid>.json, one JSON object per line.
func (w *WhatsAppClient) appendMessages(jidStr string, msgs []savedMessage) error {
	if len(msgs) == 0 {
		return nil
	}
	
	w.muStoreFile.Lock()
	defer w.muStoreFile.Unlock()

	storeDir := filepath.Join(".", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		return fmt.Errorf("failed to create store directory: %w", err)
	}
	
	path := filepath.Join(storeDir, fmt.Sprintf("msg_%s.json", jidStr))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open message file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, m := range msgs {
		if err := enc.Encode(m); err != nil {
			return fmt.Errorf("failed to encode message: %w", err)
		}
	}
	
	return nil
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

		seen := w.loadMessageIDs(jidStr)
		var batch []savedMessage

		for _, hm := range hmsgs {
			wmsg := hm.GetMessage()
			if wmsg == nil {
				continue
			}
			body := wmsg.GetMessage()
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
			if id != "" && seen[id] {
				continue
			}

			sender := key.GetParticipant()
			if sender == "" {
				if key.GetFromMe() && w.client != nil && w.client.Store.ID != nil {
					sender = w.client.Store.ID.String()
				} else {
					sender = jidStr
				}
			}
			pushName := wmsg.GetPushName()

			rec := savedMessage{
				ID:         id,
				ChatJID:    jidStr,
				SenderJID:  sender,
				SenderName: pushName,
				Text:       text,
				Timestamp:  int64(wmsg.GetMessageTimestamp()),
				FromMe:     key.GetFromMe(),
				MediaType:  mediaType,
				Mimetype:   mime,
				FileName:   fileName,
				FileSize:   size,
				Width:      mw,
				Height:     mh,
				Duration:   dur,
			}
			if len(thumb) > 0 {
				rec.ThumbB64 = base64.StdEncoding.EncodeToString(thumb)
			}
			batch = append(batch, rec)
			if id != "" {
				seen[id] = true
			}
		}

		if len(batch) == 0 {
			continue
		}
		// HistorySync delivers newest-first; persist in chronological order
		// so file appends remain time-ordered overall.
		sort.Slice(batch, func(i, j int) bool {
			return batch[i].Timestamp < batch[j].Timestamp
		})
		w.appendMessages(jidStr, batch)
		totalNew += len(batch)
	}

	if totalNew > 0 {
		log.Printf("HistorySync: imported %d new messages across %d chats", totalNew, len(convs))
		if w.OnHistoryUpdate != nil {
			w.OnHistoryUpdate()
		}
	}
}

// SendMessage sends a text message to a chat
func (w *WhatsAppClient) SendMessage(jid types.JID, text string) error {
	if !w.IsConnected() {
		return fmt.Errorf("not connected to WhatsApp")
	}

	_, err := w.client.SendMessage(context.Background(), jid, &waE2E.Message{
		Conversation: proto.String(text),
	})
	return err
}

// SendImage sends an image message with a JPEG thumbnail and correct dimensions
// so recipients see a proper preview before downloading.
func (w *WhatsAppClient) SendImage(jid types.JID, path string, caption string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read image: %w", err)
	}

	resp, err := w.client.Upload(context.Background(), data, whatsmeow.MediaImage)
	if err != nil {
		return fmt.Errorf("upload image: %w", err)
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

	_, err = w.client.SendMessage(context.Background(), jid, &waE2E.Message{
		ImageMessage: imageMsg,
	})
	return err
}

// SendFile sends a file message
func (w *WhatsAppClient) SendFile(jid types.JID, path string, filename string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	mime := http.DetectContentType(data)
	resp, err := w.client.Upload(context.Background(), data, whatsmeow.MediaDocument)
	if err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}

	_, err = w.client.SendMessage(context.Background(), jid, &waE2E.Message{
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
	return err
}

// SendAudio sends an audio/voice message (OPUS format)
func (w *WhatsAppClient) SendAudio(jid types.JID, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read audio file: %w", err)
	}

	resp, err := w.client.Upload(context.Background(), data, whatsmeow.MediaAudio)
	if err != nil {
		return fmt.Errorf("failed to upload audio: %w", err)
	}

	_, err = w.client.SendMessage(context.Background(), jid, &waE2E.Message{
		AudioMessage: &waE2E.AudioMessage{
			URL:           &resp.URL,
			DirectPath:    &resp.DirectPath,
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			Mimetype:      proto.String("audio/ogg; codecs=opus"),
		},
	})
	return err
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
