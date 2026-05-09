package client

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"math"
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

// AvatarPath returns the post-processed (circular PNG) path for a JID's
// avatar. Caller should os.Stat to verify the file exists.
func AvatarPath(jidStr string) string {
	safe := strings.ReplaceAll(jidStr, ":", "_")
	return filepath.Join(MediaDir(), avatarSubdir, safe+".png")
}

// avatarLegacyJPEGPath is the pre-circular JPEG path. Kept for migration
// from earlier app versions that stored rectangular JPGs.
func avatarLegacyJPEGPath(jidStr string) string {
	safe := strings.ReplaceAll(jidStr, ":", "_")
	return filepath.Join(MediaDir(), avatarSubdir, safe+".jpg")
}

// CachedAvatar returns the on-disk path for the best available avatar:
// prefers the circular PNG, falls back to a legacy JPEG (still functional
// — just rectangular — until the next EnsureAvatar migrates it).
func CachedAvatar(jidStr string) string {
	if p := AvatarPath(jidStr); fileExists(p) {
		return p
	}
	if p := avatarLegacyJPEGPath(jidStr); fileExists(p) {
		return p
	}
	return ""
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Size() > 0
}

// MigrateLegacyAvatars converts any pre-circular JPEG avatars in
// media/avatars/ to circular PNGs and removes the originals. Idempotent;
// safe to call on every startup.
func MigrateLegacyAvatars() {
	dir := filepath.Join(MediaDir(), avatarSubdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	migrated := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jpg") {
			continue
		}
		jpegPath := filepath.Join(dir, e.Name())
		pngPath := strings.TrimSuffix(jpegPath, ".jpg") + ".png"
		if fileExists(pngPath) {
			continue // already migrated
		}
		if err := makeCircular(jpegPath, pngPath); err != nil {
			log.Printf("avatar migrate %s: %v", e.Name(), err)
			continue
		}
		_ = os.Remove(jpegPath)
		migrated++
	}
	if migrated > 0 {
		log.Printf("MigrateLegacyAvatars: converted %d JPGs to circular PNGs", migrated)
	}
}

// makeCircular reads srcPath (any supported image format), center-crops to a
// square, applies a circular alpha mask with 1-pixel anti-aliasing at the
// edge, and writes the result as PNG to dstPath.
func makeCircular(srcPath, dstPath string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	side := w
	if h < side {
		side = h
	}
	if side <= 0 {
		return fmt.Errorf("zero-size image")
	}

	sx := bounds.Min.X + (w-side)/2
	sy := bounds.Min.Y + (h-side)/2

	out := image.NewNRGBA(image.Rect(0, 0, side, side))
	cx := float64(side) / 2.0
	cy := float64(side) / 2.0
	radius := float64(side) / 2.0
	radiusSq := radius * radius

	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			dx := float64(x) - cx + 0.5
			dy := float64(y) - cy + 0.5
			distSq := dx*dx + dy*dy
			if distSq > radiusSq {
				continue
			}
			r, g, b, a := img.At(sx+x, sy+y).RGBA()
			alpha := float64(a >> 8)
			// Soft AA at the edge: fade alpha over the outer pixel.
			edge := math.Sqrt(distSq) / radius
			if edge > 0.97 {
				fade := 1 - (edge-0.97)/0.03
				alpha *= fade
			}
			out.SetNRGBA(x, y, color.NRGBA{
				R: uint8(r >> 8),
				G: uint8(g >> 8),
				B: uint8(b >> 8),
				A: uint8(alpha),
			})
		}
	}

	o, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer o.Close()
	return png.Encode(o, out)
}

// EnsureAvatar fetches and caches the JID's profile picture as a circular
// PNG. Idempotent: returns immediately if the .png is on disk; if a legacy
// .jpg exists it's converted in place. Otherwise downloads, applies the
// circular mask, and writes the .png.
//
// Designed to be called from background goroutines per chat — concurrent
// callers for the same JID coalesce on the inflight channel.
func (w *WhatsAppClient) EnsureAvatar(jidStr string) string {
	if w.client == nil || !w.IsConnected() {
		return CachedAvatar(jidStr)
	}

	pngPath := AvatarPath(jidStr)
	if fileExists(pngPath) {
		return pngPath
	}

	// Legacy migration: a previous app version saved rectangular JPGs.
	// Convert in place and drop the original.
	jpegPath := avatarLegacyJPEGPath(jidStr)
	if fileExists(jpegPath) {
		if err := makeCircular(jpegPath, pngPath); err == nil {
			_ = os.Remove(jpegPath)
			return pngPath
		}
		// fallthrough to redownload on conversion failure
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

	if err := os.MkdirAll(filepath.Dir(pngPath), 0755); err != nil {
		log.Printf("avatar: mkdir %s: %v", filepath.Dir(pngPath), err)
		return ""
	}

	// Download to a temp .jpg, then process into the canonical .png.
	tempPath := jpegPath
	if err := downloadAvatarURL(info.URL, tempPath); err != nil {
		log.Printf("avatar download %s: %v", jidStr, err)
		w.markAvatarFailed(jidStr)
		return ""
	}
	if err := makeCircular(tempPath, pngPath); err != nil {
		log.Printf("avatar circularize %s: %v", jidStr, err)
		// Keep the rectangular jpg so something renders; CachedAvatar finds it.
		return tempPath
	}
	_ = os.Remove(tempPath)
	return pngPath
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
