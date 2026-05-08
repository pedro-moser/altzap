# Receive Media Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cliente baixa, persiste e renderiza inline imagens, áudios, vídeos, documentos e stickers recebidos. Mídia permanece disponível após restart do app.

**Architecture:**
- Estender `savedMessage` JSONL com campos de mídia (`media_type`, `media_path`, `mimetype`, `width`, `height`, `duration`, `caption`, `thumb_b64`).
- Novo arquivo `client/media.go` orquestra download via `whatsmeow.Client.Download`, salva em `media/<chat_jid>/<msg_id>.<ext>`, idempotente.
- Quando mensagem com mídia chega: persistir record imediato com `media_path` vazio + thumbnail JPEG embutido; goroutine baixa em background; quando termina, atualiza record (rewrite do JSONL) e dispara `OnMediaReady`.
- UI: extender `Message` struct, `buildMessageBubble` despacha por `MediaType` para renderers em novo `ui/media_bubble.go`. V1 abre via `xdg-open` ao clicar; preview inline com `JPEGThumbnail`.

**Tech Stack:** whatsmeow `Client.Download`, Fyne `canvas.Image` + `widget.NewButtonWithIcon`, Go stdlib (`os/exec` para `xdg-open`).

---

## File Structure

### Created
| Path | Responsibility |
|---|---|
| `client/media.go` | Download orchestrator: `downloadMedia`, path/mime helpers, extensão por mime, idempotência por filesystem check |
| `ui/media_bubble.go` | Renderers especializados de bubble por tipo (`buildImageBubble`, `buildAudioBubble`, `buildVideoBubble`, `buildDocBubble`) + helper `openExternal` para xdg-open |
| `media/` (runtime) | Cache de mídia baixada, gitignored, criado on demand |

### Modified
| Path | Mudança |
|---|---|
| `client/whatsmeow_client.go` | `savedMessage` ganha campos de mídia; `saveMessage` deixa de retornar early para mídia; novo helper `extractMedia`; `eventHandler` dispara download em goroutine; `handleHistorySync` inclui mídia |
| `ui/chat_view.go` | `Message` struct ganha campos de mídia; `buildMessageBubble` delega ao despachador; `loadMessagesFromDisk` passa novos campos; `AddMessage` aceita mídia |

---

## Task 1: Schema savedMessage e MessageEvent estendidos

**Files:**
- Modify: `client/whatsmeow_client.go` (struct savedMessage linhas 26-33, MessageEvent 38-47)

- [ ] **Step 1: Estender struct `savedMessage` e `MessageEvent`**

Substituir as definições atuais por:

```go
// savedMessage is the on-disk JSON record for a chat message.
type savedMessage struct {
	ID         string `json:"id,omitempty"`
	ChatJID    string `json:"chat_jid"`
	SenderJID  string `json:"sender_jid"`
	SenderName string `json:"sender_name,omitempty"` // PushName at time of save (no in-text prefix)
	Text       string `json:"text"`                  // body text or caption (no prefix)
	Timestamp  int64  `json:"timestamp"`
	FromMe     bool   `json:"from_me"`

	// Media (optional). When MediaType != "", treat as media message.
	MediaType string `json:"media_type,omitempty"` // image|video|audio|document|sticker|voice
	MediaPath string `json:"media_path,omitempty"` // relative path under project; "" until downloaded
	Mimetype  string `json:"mimetype,omitempty"`
	FileName  string `json:"filename,omitempty"`
	FileSize  uint64 `json:"file_size,omitempty"`
	Width     uint32 `json:"width,omitempty"`
	Height    uint32 `json:"height,omitempty"`
	Duration  uint32 `json:"duration,omitempty"` // seconds
	ThumbB64  string `json:"thumb_b64,omitempty"` // base64 of JPEGThumbnail for instant preview
}

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
```

- [ ] **Step 2: Adicionar callback `OnMediaReady` ao client**

Em `WhatsAppClient` struct (linha 69), adicionar campo:

```go
OnMediaReady    func(chatJID, msgID, mediaPath string)
```

E em `MessageEvent` rendering, UI registra callback que atualiza bubble.

- [ ] **Step 3: Confirmar que código compila**

Run: `go build ./...`
Expected: campos novos compilam; usos antigos de `MediaType`/`MediaData` em `eventHandler` (linha 220) precisam ser atualizados. Apagar campo `MediaData` do struct (não usado).

- [ ] **Step 4: Commit**

```bash
git add client/whatsmeow_client.go
git commit -m "feat(client): extend savedMessage schema for media fields"
```

---

## Task 2: Helper `extractMedia` + dispatch em `eventHandler`

**Files:**
- Modify: `client/whatsmeow_client.go` (eventHandler 188-253, novo helper)

- [ ] **Step 1: Adicionar helper `extractMedia` antes de `eventHandler`**

```go
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
```

- [ ] **Step 2: Reescrever `eventHandler` completo (linhas 188-253)**

```go
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
		text = caption // image/video caption falls back to text
	}

	messageEvent := MessageEvent{
		Info:       msg.Info,
		Text:       text,
		SenderJid:  sender,
		Timestamp:  ts.Unix(),
		IsGroup:    msg.Info.IsGroup,
		SenderName: msg.Info.PushName,
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

	// Persist immediately with empty MediaPath; download fills it asynchronously.
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
```

- [ ] **Step 3: Substituir `saveMessage` por `persistIncoming`** (deletar antigo, adicionar novo)

```go
// persistIncoming writes a savedMessage record to disk for a freshly-arrived
// event.Message. MediaPath is empty here; downloadAndPatch fills it later.
func (w *WhatsAppClient) persistIncoming(msg *events.Message, mediaType, mime, fileName string, size uint64, width, height, duration uint32, thumb []byte, text string) {
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
	if text == "" && mediaType == "" {
		return // nothing to save
	}
	if err := w.appendMessages(rec.ChatJID, []savedMessage{rec}); err != nil {
		log.Printf("persistIncoming: %v", err)
	}
}
```

Adicionar `"encoding/base64"` aos imports.

- [ ] **Step 4: Remover método `saveMessage` antigo (linhas 272-292) e seu chamador**

Remover `w.saveMessage(msg)` no final de `eventHandler` (foi substituído por `persistIncoming` chamada acima). Apagar `func (w *WhatsAppClient) saveMessage`.

- [ ] **Step 5: Compilação**

Run: `go build ./...`
Expected: erros sobre `downloadAndPatch` não definido — esperado, será criado em Task 3.

- [ ] **Step 6: Commit (parcial — não compila ainda; ok porque proximo task fecha)**

```bash
git add client/whatsmeow_client.go
git commit -m "feat(client): extract media metadata and persist on receive"
```

---

## Task 3: `client/media.go` — download orchestrator

**Files:**
- Create: `client/media.go`

- [ ] **Step 1: Criar `client/media.go`**

```go
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
)

const mediaDir = "media"

// inflightDownloads dedupes concurrent download requests for the same msgID.
var (
	inflightMu sync.Mutex
	inflight   = make(map[string]chan struct{})
)

// extForMime maps a MIME type to a sensible file extension.
func extForMime(mime string) string {
	mime = strings.ToLower(mime)
	switch {
	case strings.HasPrefix(mime, "image/jpeg"):
		return ".jpg"
	case strings.HasPrefix(mime, "image/png"):
		return ".png"
	case strings.HasPrefix(mime, "image/webp"):
		return ".webp"
	case strings.HasPrefix(mime, "image/gif"):
		return ".gif"
	case strings.HasPrefix(mime, "video/mp4"), strings.HasPrefix(mime, "video/"):
		return ".mp4"
	case strings.Contains(mime, "ogg"), strings.Contains(mime, "opus"):
		return ".ogg"
	case strings.HasPrefix(mime, "audio/mpeg"):
		return ".mp3"
	case strings.HasPrefix(mime, "audio/mp4"), strings.HasPrefix(mime, "audio/aac"):
		return ".m4a"
	case strings.HasPrefix(mime, "audio/wav"), strings.HasPrefix(mime, "audio/x-wav"):
		return ".wav"
	case strings.HasPrefix(mime, "application/pdf"):
		return ".pdf"
	}
	return ".bin"
}

// mediaPath computes target path under media/<chat>/<id>.<ext>.
// chatJID may contain ":" or "@" — those are filesystem-safe on Linux/macOS.
func mediaPath(chatJID, msgID, ext string) string {
	if msgID == "" {
		msgID = "unknown"
	}
	return filepath.Join(mediaDir, chatJID, msgID+ext)
}

// pickDownloadable extracts the first media-bearing field from a Message.
// Returns nil if the message has no downloadable media.
func pickDownloadable(m *waE2E.Message) whatsmeow.DownloadableMessage {
	switch {
	case m.ImageMessage != nil:
		return m.ImageMessage
	case m.VideoMessage != nil:
		return m.VideoMessage
	case m.AudioMessage != nil:
		return m.AudioMessage
	case m.DocumentMessage != nil:
		return m.DocumentMessage
	case m.StickerMessage != nil:
		return m.StickerMessage
	}
	return nil
}

// downloadAndPatch downloads the media for msg and patches the JSONL store
// to fill in the MediaPath of the matching record. Idempotent: skips if
// the file already exists.
func (w *WhatsAppClient) downloadAndPatch(msg *events.Message) {
	if w.client == nil {
		return
	}
	dl := pickDownloadable(msg.Message)
	if dl == nil {
		return
	}
	mediaType, mime, _, _, _, _, _, _, _ := extractMedia(msg.Message)
	if mediaType == "" {
		return
	}

	chatJID := msg.Info.Chat.String()
	msgID := msg.Info.ID
	ext := extForMime(mime)
	target := mediaPath(chatJID, msgID, ext)

	// Inflight dedup
	inflightMu.Lock()
	if ch, ok := inflight[msgID]; ok {
		inflightMu.Unlock()
		<-ch
		return
	}
	done := make(chan struct{})
	inflight[msgID] = done
	inflightMu.Unlock()
	defer func() {
		inflightMu.Lock()
		delete(inflight, msgID)
		close(done)
		inflightMu.Unlock()
	}()

	// Idempotent: skip if already on disk
	if info, err := os.Stat(target); err == nil && info.Size() > 0 {
		w.patchMediaPath(chatJID, msgID, target)
		w.fireMediaReady(chatJID, msgID, target)
		return
	}

	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		log.Printf("download: mkdir %s: %v", filepath.Dir(target), err)
		return
	}

	data, err := w.client.Download(context.Background(), dl)
	if err != nil {
		log.Printf("download %s/%s: %v", chatJID, msgID, err)
		return
	}
	if err := os.WriteFile(target, data, 0644); err != nil {
		log.Printf("download: write %s: %v", target, err)
		return
	}

	w.patchMediaPath(chatJID, msgID, target)
	w.fireMediaReady(chatJID, msgID, target)
}

func (w *WhatsAppClient) fireMediaReady(chatJID, msgID, path string) {
	if w.OnMediaReady != nil {
		w.OnMediaReady(chatJID, msgID, path)
	}
}

// patchMediaPath rewrites the JSONL store for a chat to set MediaPath on the
// record matching msgID. Atomic via temp-file rename.
func (w *WhatsAppClient) patchMediaPath(chatJID, msgID, path string) {
	w.muStoreFile.Lock()
	defer w.muStoreFile.Unlock()

	src := filepath.Join(".", "store", fmt.Sprintf("msg_%s.json", chatJID))
	in, err := os.Open(src)
	if err != nil {
		return
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(src), "msg_*.tmp")
	if err != nil {
		return
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()

	dec := json.NewDecoder(in)
	enc := json.NewEncoder(tmp)
	patched := false
	for dec.More() {
		var rec savedMessage
		if err := dec.Decode(&rec); err != nil {
			continue
		}
		if rec.ID == msgID && rec.MediaPath == "" {
			rec.MediaPath = path
			patched = true
		}
		if err := enc.Encode(&rec); err != nil {
			return
		}
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if patched {
		os.Rename(tmp.Name(), src)
	}
}
```

- [ ] **Step 2: Compilar**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add client/media.go
git commit -m "feat(client): media download orchestrator with idempotent JSONL patch"
```

---

## Task 4: HistorySync inclui mídia

**Files:**
- Modify: `client/whatsmeow_client.go` (handleHistorySync linha 372)

- [ ] **Step 1: Reescrever bloco de iteração de mensagens em `handleHistorySync`**

Substituir o loop em `handleHistorySync` (das linhas ~413 a ~454) por uma versão que aceita mídia:

```go
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
				continue // nothing of substance
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
```

NOTE: HistorySync messages don't carry `*events.Message`, só `*waHistorySync.HistorySyncMsg`. Download dessas mensagens é mais complexo e fica para uma fase futura — historicamente baixar todas as mídias do histórico explode I/O. Por ora persistimos o thumbnail (preview rápido) e na primeira reabertura do chat, se usuário quiser baixar pode requisitar manualmente (Task fora de escopo deste plano).

- [ ] **Step 2: Compilar**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add client/whatsmeow_client.go
git commit -m "feat(client): persist media metadata from HistorySync"
```

---

## Task 5: UI Message struct + loadMessagesFromDisk

**Files:**
- Modify: `ui/chat_view.go` (Message struct linha 25, loadMessagesFromDisk linha 814, AddMessage linha 921)

- [ ] **Step 1: Estender struct `Message`**

Substituir struct atual (linhas 25-30):

```go
type Message struct {
	ID        string
	Sender    string
	Text      string
	Timestamp time.Time
	IsOwn     bool

	MediaType string // image|video|audio|voice|document|sticker
	MediaPath string // local file path; "" if still downloading
	Mimetype  string
	FileName  string
	FileSize  uint64
	Width     uint32
	Height    uint32
	Duration  uint32
	Thumb     []byte
}
```

- [ ] **Step 2: Reescrever `loadMessagesFromDisk` para popular novos campos**

Substituir corpo de `loadMessagesFromDisk` (linhas 814-863):

```go
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

		// Resolve sender display name. Prefer explicit sender_name (new schema).
		// Fallback: parse legacy "Name: text" prefix if present.
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
```

Adicionar `"encoding/base64"` aos imports do chat_view.go.

- [ ] **Step 3: Estender `AddMessage` para passar mídia**

Substituir corpo (linhas 921-962):

```go
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

	text := msg.Text
	newMsg := &Message{
		ID:        msg.Info.ID,
		Sender:    senderName,
		Text:      text,
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
	go func() {
		cv.loadChatList()
		cv.refreshChats()
	}()
}
```

- [ ] **Step 4: Compilar**

Run: `go build ./...`
Expected: erros sobre `buildMessageBubble` não dispatching media — esperado, fixed em Task 6.

- [ ] **Step 5: Commit**

```bash
git add ui/chat_view.go
git commit -m "feat(ui): extend Message struct + loaders to carry media metadata"
```

---

## Task 6: `ui/media_bubble.go` — renderers de bubble por tipo

**Files:**
- Create: `ui/media_bubble.go`

- [ ] **Step 1: Criar `ui/media_bubble.go` com renderers e helpers**

```go
package ui

import (
	"fmt"
	"image"
	"os/exec"
	"runtime"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// openExternal launches the OS default handler for the given file path.
func openExternal(path string) {
	if path == "" {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", path)
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		return
	}
	_ = cmd.Start()
	go cmd.Wait()
}

func humanizeBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n2 := n / unit; n2 >= unit; n2 /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// imageContent returns a canvas.Image for the bubble, preferring the local
// file once downloaded, falling back to the embedded thumbnail bytes.
func imageContent(msg *Message) fyne.CanvasObject {
	var img *canvas.Image
	switch {
	case msg.MediaPath != "":
		img = canvas.NewImageFromFile(msg.MediaPath)
	case len(msg.Thumb) > 0:
		img = canvas.NewImageFromResource(fyne.NewStaticResource("thumb_"+msg.ID, msg.Thumb))
	default:
		// Placeholder: use file image icon
		return widget.NewIcon(theme.FileImageIcon())
	}
	img.FillMode = canvas.ImageFillContain
	w, h := previewDims(msg.Width, msg.Height)
	img.SetMinSize(fyne.NewSize(w, h))
	return img
}

// previewDims caps preview size to a sensible bubble area, preserving aspect.
func previewDims(w, h uint32) (float32, float32) {
	const maxW, maxH float32 = 320, 320
	if w == 0 || h == 0 {
		return 240, 180
	}
	fw, fh := float32(w), float32(h)
	scale := float32(1)
	if fw/fh > maxW/maxH {
		scale = maxW / fw
	} else {
		scale = maxH / fh
	}
	return fw * scale, fh * scale
}

func buildImageBubble(msg *Message) fyne.CanvasObject {
	content := imageContent(msg)

	openBtn := widget.NewButtonWithIcon("Open", theme.ZoomFitIcon(), func() {
		openExternal(msg.MediaPath)
	})
	openBtn.Importance = widget.LowImportance
	if msg.MediaPath == "" {
		openBtn.Disable()
	}

	rows := []fyne.CanvasObject{content}
	if msg.Text != "" {
		caption := widget.NewLabel(msg.Text)
		caption.Wrapping = fyne.TextWrapWord
		rows = append(rows, caption)
	}
	rows = append(rows, container.NewHBox(openBtn))
	return container.NewVBox(rows...)
}

func buildVideoBubble(msg *Message) fyne.CanvasObject {
	var thumb fyne.CanvasObject
	if len(msg.Thumb) > 0 {
		img := canvas.NewImageFromResource(fyne.NewStaticResource("vthumb_"+msg.ID, msg.Thumb))
		img.FillMode = canvas.ImageFillContain
		w, h := previewDims(msg.Width, msg.Height)
		img.SetMinSize(fyne.NewSize(w, h))
		thumb = img
	} else {
		ic := widget.NewIcon(theme.MediaVideoIcon())
		thumb = container.NewGridWrap(fyne.NewSize(240, 180), ic)
	}

	overlay := canvas.NewText("▶", whiteColor)
	overlay.TextSize = 36
	overlay.TextStyle.Bold = true
	stacked := container.NewStack(thumb, container.NewCenter(overlay))

	durTxt := ""
	if msg.Duration > 0 {
		durTxt = fmt.Sprintf("  %d:%02d", msg.Duration/60, msg.Duration%60)
	}
	openBtn := widget.NewButtonWithIcon("Play"+durTxt, theme.MediaPlayIcon(), func() {
		openExternal(msg.MediaPath)
	})
	openBtn.Importance = widget.LowImportance
	if msg.MediaPath == "" {
		openBtn.Disable()
		openBtn.SetText("Downloading…" + durTxt)
	}

	rows := []fyne.CanvasObject{stacked}
	if msg.Text != "" {
		caption := widget.NewLabel(msg.Text)
		caption.Wrapping = fyne.TextWrapWord
		rows = append(rows, caption)
	}
	rows = append(rows, container.NewHBox(openBtn))
	return container.NewVBox(rows...)
}

func buildAudioBubble(msg *Message) fyne.CanvasObject {
	icon := theme.MediaMusicIcon()
	if msg.MediaType == "voice" {
		icon = theme.AccountIcon() // mic-ish
	}

	durTxt := "—:—"
	if msg.Duration > 0 {
		durTxt = fmt.Sprintf("%d:%02d", msg.Duration/60, msg.Duration%60)
	}
	label := widget.NewLabel(durTxt)

	playBtn := widget.NewButtonWithIcon("", theme.MediaPlayIcon(), func() {
		openExternal(msg.MediaPath)
	})
	if msg.MediaPath == "" {
		playBtn.Disable()
	}

	row := container.NewBorder(nil, nil,
		widget.NewIcon(icon),
		label,
		playBtn,
	)
	return container.NewGridWrap(fyne.NewSize(260, 44), row)
}

func buildDocBubble(msg *Message) fyne.CanvasObject {
	name := msg.FileName
	if name == "" {
		name = "Document"
	}
	nameLbl := widget.NewLabel(name)
	nameLbl.TextStyle.Bold = true
	sizeLbl := widget.NewLabel(humanizeBytes(msg.FileSize))
	sizeLbl.Importance = widget.LowImportance

	openBtn := widget.NewButtonWithIcon("Open", theme.DocumentIcon(), func() {
		openExternal(msg.MediaPath)
	})
	if msg.MediaPath == "" {
		openBtn.SetText("Downloading…")
		openBtn.Disable()
	}

	textCol := container.NewVBox(nameLbl, sizeLbl)
	row := container.NewBorder(nil, nil,
		widget.NewIcon(theme.FileIcon()),
		openBtn,
		textCol,
	)
	return container.NewGridWrap(fyne.NewSize(320, 60), row)
}

func buildStickerBubble(msg *Message) fyne.CanvasObject {
	if msg.MediaPath != "" {
		img := canvas.NewImageFromFile(msg.MediaPath)
		img.FillMode = canvas.ImageFillContain
		img.SetMinSize(fyne.NewSize(120, 120))
		return img
	}
	if len(msg.Thumb) > 0 {
		img := canvas.NewImageFromResource(fyne.NewStaticResource("sthumb_"+msg.ID, msg.Thumb))
		img.FillMode = canvas.ImageFillContain
		img.SetMinSize(fyne.NewSize(120, 120))
		return img
	}
	return widget.NewIcon(theme.FileImageIcon())
}

// _ silences unused import if `image` is not referenced — kept for future use.
var _ = image.Black
```

- [ ] **Step 2: Compilar**

Run: `go build ./...`
Expected: erros sobre buildMessageBubble não dispatchando — fixed na próxima task.

- [ ] **Step 3: Commit**

```bash
git add ui/media_bubble.go
git commit -m "feat(ui): media bubble renderers (image/video/audio/doc/sticker)"
```

---

## Task 7: Despachar `buildMessageBubble` por tipo + register `OnMediaReady`

**Files:**
- Modify: `ui/chat_view.go` (buildMessageBubble linha 387, NewChatView linha 132)

- [ ] **Step 1: Reescrever `buildMessageBubble` para delegar a media renderers**

Substituir a função atual (linhas 387-441):

```go
func (cv *ChatView) buildMessageBubble(msg *Message) fyne.CanvasObject {
	bubbleBg := canvas.NewRectangle(otherMsgBgColor)
	if msg.IsOwn {
		bubbleBg.FillColor = ownMsgBgColor
	}
	bubbleBg.CornerRadius = 8

	parts := make([]fyne.CanvasObject, 0, 3)
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

	// Media branch
	if msg.MediaType != "" {
		var media fyne.CanvasObject
		switch msg.MediaType {
		case "image":
			media = buildImageBubble(msg)
		case "video":
			media = buildVideoBubble(msg)
		case "audio", "voice":
			media = buildAudioBubble(msg)
		case "document":
			media = buildDocBubble(msg)
		case "sticker":
			media = buildStickerBubble(msg)
		}
		if media != nil {
			parts = append(parts, media)
			if w := media.MinSize().Width; w > naturalContentWidth {
				naturalContentWidth = w
			}
		}
	} else if msg.Text != "" {
		// Text-only branch (existing behavior)
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
```

- [ ] **Step 2: Registrar `OnMediaReady` em `NewChatView` para refresh quando download completa**

Adicionar após `cv.waClient.FetchContacts()` (linha 140):

```go
	// Refresh bubble when media download finishes; in-place edit of Message
	// + bubble rebuild for the open chat.
	cv.waClient.OnMediaReady = func(chatJID, msgID, path string) {
		cv.muMessages.Lock()
		msgs := cv.messages[chatJID]
		for _, m := range msgs {
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
```

- [ ] **Step 3: Compilar e rodar**

Run: `go build ./...`
Expected: PASS

Run: `go run .`
Expected: app abre, login funciona (whatsapp.db ainda válido), recebe mensagem com mídia → mostra placeholder → após download vira preview.

- [ ] **Step 4: Commit**

```bash
git add ui/chat_view.go
git commit -m "feat(ui): dispatch bubble rendering by media type and refresh on download"
```

---

## Task 8: Smoke test

**Files:** none

- [ ] **Step 1: Build limpo**

```bash
go build -o whatsappalt_dev .
```

Expected: binário ~50MB, sem warnings.

- [ ] **Step 2: Subir app, validar login com sessão existente**

```bash
./whatsappalt_dev
```

Expected: lista de chats existente carrega sem QR (whatsapp.db válido).

- [ ] **Step 3: Pedir Pedro para enviar mídia ao seu próprio número (ou abrir chat com mídia recente)**

Validar:
- Imagem aparece com preview do JPEG thumbnail imediato
- Após download (~segundos): preview vira full image
- Botão "Open" abre no visualizador padrão
- Áudio: botão play abre no player padrão (ou ffplay)
- Documento: nome + tamanho + Open
- Mídia persiste após restart (validar reabrindo app)

- [ ] **Step 4: Commit do plano executado se tudo funciona**

```bash
git add docs/superpowers/plans/2026-05-08-receive-media.md
git commit -m "docs: receive-media plan (executed)"
```

---

## Self-Review

**1. Spec coverage:**
- Goal: receber mídia inline e persistir → coberto pelas Tasks 2 (extract+download), 3 (orquestrador), 5 (UI struct), 6 (renderers), 7 (dispatch).
- Persistir mídia através de restart → coberto por Task 4 (HistorySync) + Task 3 (atomic patch).

**2. Placeholders:** Nenhum "TBD" / "implement later" — todo código vai pra disco verbatim.

**3. Type consistency:** `savedMessage` campos batem com struct tag JSON, `Message` UI carrega mesmos campos, `MessageEvent` carrega mesmo set, `extractMedia` retorna mesmos tipos.

**4. Riscos não cobertos pelo plano (deferred):**
- Download de mídia em HistorySync (custo I/O alto). Persistimos só metadata + thumbnail; download on-demand fica para fase futura.
- Inline audio player (V1.5).
- Tappable image fullscreen (V1.5).
- Reactions, reply, edits (next plan).
