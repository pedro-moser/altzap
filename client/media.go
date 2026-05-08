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

// inflightDownloads dedupes concurrent download requests by msgID. Many code
// paths can race to download the same message (eventHandler + a future
// HistorySync-triggered backfill), and we don't want to pay double bandwidth.
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
	case strings.HasPrefix(mime, "video/"):
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

// mediaPath builds the on-disk path for a downloaded media file. chatJID may
// contain '@', ':' — those are filesystem-safe on Linux/macOS; on Windows we
// substitute. msgID is trusted to be alphanumeric per WhatsApp conventions.
func mediaPath(chatJID, msgID, ext string) string {
	if msgID == "" {
		msgID = "unknown"
	}
	chatDir := strings.ReplaceAll(chatJID, ":", "_")
	return filepath.Join(mediaDir, chatDir, msgID+ext)
}

// pickDownloadable returns the first media-bearing field from a Message proto.
// nil if the message has no downloadable media.
func pickDownloadable(m *waE2E.Message) whatsmeow.DownloadableMessage {
	if m == nil {
		return nil
	}
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

// downloadAndPatch downloads the media for an incoming message and patches
// the JSONL store to fill in MediaPath. Idempotent: a hit on disk short-circuits.
func (w *WhatsAppClient) downloadAndPatch(msg *events.Message) {
	if w.client == nil || msg == nil || msg.Message == nil {
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

// patchMediaPath sets MediaPath on the matching record (if currently empty).
// Convenience wrapper around patchRecord — same semantics as before.
func (w *WhatsAppClient) patchMediaPath(chatJID, msgID, path string) {
	w.patchRecord(chatJID, msgID, func(rec *SavedMessage) bool {
		if rec.MediaPath != "" {
			return false
		}
		rec.MediaPath = path
		return true
	})
}

// patchRecord finds the message record matching msgID in the chat's JSONL,
// applies mutate, and rewrites the file atomically (temp + rename) only if
// mutate returned true. Used for media-path fills, reaction updates, and
// future edit/delete features.
func (w *WhatsAppClient) patchRecord(chatJID, msgID string, mutate func(*SavedMessage) bool) {
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
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	dec := json.NewDecoder(in)
	enc := json.NewEncoder(tmp)
	patched := false
	for dec.More() {
		var rec SavedMessage
		if err := dec.Decode(&rec); err != nil {
			continue
		}
		if rec.ID == msgID {
			if mutate(&rec) {
				patched = true
			}
		}
		if err := enc.Encode(&rec); err != nil {
			tmp.Close()
			return
		}
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if patched {
		_ = os.Rename(tmpName, src)
	}
}
