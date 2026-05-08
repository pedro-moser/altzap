package client

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

const avatarSubdir = "avatars"

// avatarFetchInflight dedupes concurrent fetches per JID.
var (
	avatarMu       sync.Mutex
	avatarInflight = make(map[string]chan struct{})

	// negativeCache: JIDs we tried to fetch and got "no profile picture" or
	// an error. We avoid hammering the server on every render.
	avatarNegMu  sync.RWMutex
	avatarNegTTL = 6 * time.Hour
	avatarNeg    = make(map[string]time.Time)
)

// AvatarPath returns the on-disk path for a JID's profile picture, regardless
// of whether the file actually exists. Caller should os.Stat to verify.
func AvatarPath(jidStr string) string {
	safe := strings.ReplaceAll(jidStr, ":", "_")
	return filepath.Join(mediaDir, avatarSubdir, safe+".jpg")
}

// CachedAvatar returns the on-disk path if a downloaded avatar exists, else "".
func CachedAvatar(jidStr string) string {
	p := AvatarPath(jidStr)
	if info, err := os.Stat(p); err == nil && info.Size() > 0 {
		return p
	}
	return ""
}

// EnsureAvatar fetches and caches the JID's profile picture. Idempotent: if
// the file already exists on disk, returns the path immediately. On miss,
// blocks while fetching, then returns the path (or "" if no avatar / error).
//
// Designed to be called from background goroutines per chat — concurrent
// callers for the same JID coalesce on the inflight channel.
func (w *WhatsAppClient) EnsureAvatar(jidStr string) string {
	if w.client == nil || !w.IsConnected() {
		return CachedAvatar(jidStr)
	}
	if p := CachedAvatar(jidStr); p != "" {
		return p
	}
	if w.recentlyFailedAvatar(jidStr) {
		return ""
	}

	avatarMu.Lock()
	if ch, ok := avatarInflight[jidStr]; ok {
		avatarMu.Unlock()
		<-ch
		return CachedAvatar(jidStr)
	}
	done := make(chan struct{})
	avatarInflight[jidStr] = done
	avatarMu.Unlock()
	defer func() {
		avatarMu.Lock()
		delete(avatarInflight, jidStr)
		close(done)
		avatarMu.Unlock()
	}()

	jid, err := types.ParseJID(jidStr)
	if err != nil {
		w.markAvatarFailed(jidStr)
		return ""
	}

	info, err := w.client.GetProfilePictureInfo(context.Background(), jid, &whatsmeow.GetProfilePictureParams{
		Preview: true,
	})
	if err != nil || info == nil || info.URL == "" {
		w.markAvatarFailed(jidStr)
		return ""
	}

	target := AvatarPath(jidStr)
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		log.Printf("avatar: mkdir %s: %v", filepath.Dir(target), err)
		return ""
	}

	if err := downloadAvatarURL(info.URL, target); err != nil {
		log.Printf("avatar download %s: %v", jidStr, err)
		w.markAvatarFailed(jidStr)
		return ""
	}
	return target
}

// downloadAvatarURL fetches the URL with a short timeout and writes to target.
// Avatars are small (a few KB), so a 10s budget is generous.
func downloadAvatarURL(url, target string) error {
	cli := &http.Client{Timeout: 10 * time.Second}
	resp, err := cli.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	f, err := os.Create(target)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func (w *WhatsAppClient) recentlyFailedAvatar(jidStr string) bool {
	avatarNegMu.RLock()
	defer avatarNegMu.RUnlock()
	t, ok := avatarNeg[jidStr]
	if !ok {
		return false
	}
	return time.Since(t) < avatarNegTTL
}

func (w *WhatsAppClient) markAvatarFailed(jidStr string) {
	avatarNegMu.Lock()
	avatarNeg[jidStr] = time.Now()
	avatarNegMu.Unlock()
}
