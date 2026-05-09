package client

import (
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"sync"
)

// imgCache holds decoded image.Image keyed by file path. Once decoded, an
// image.Image is reused on every render — Fyne's canvas.Image.MinSize calls
// Decode on the file synchronously when i.Image is nil, which inside
// widget.List's SetItemHeight cascade can fire dozens of times per chat
// open. Pre-decoded images bypass that path entirely (canvas.Image checks
// `i.Image == nil` and skips Refresh when not nil).
var (
	imgCacheMu sync.RWMutex
	imgCache   = make(map[string]image.Image)
)

// CachedImage decodes path on first call and returns the cached
// image.Image on subsequent calls. Returns nil on error so callers can
// fall back to canvas.NewImageFromFile (which works correctly for tiny
// images even with the MinSize-decode path).
func CachedImage(path string) image.Image {
	if path == "" {
		return nil
	}
	imgCacheMu.RLock()
	if img, ok := imgCache[path]; ok {
		imgCacheMu.RUnlock()
		return img
	}
	imgCacheMu.RUnlock()

	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil
	}

	imgCacheMu.Lock()
	imgCache[path] = img
	imgCacheMu.Unlock()
	return img
}

// renderableImage(path) decodes and caches whatever RenderablePath returns
// for the given media path. Convenience wrapper for callers that always
// want a cache-backed canvas-ready image.
func renderableImage(path string) image.Image {
	return CachedImage(RenderablePath(path))
}
