package client

import (
	"container/list"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"sync"
)

const defaultImageCacheMaxBytes int64 = 64 << 20

type cachedImageEntry struct {
	path  string
	img   image.Image
	bytes int64
}

// imgCache holds decoded image.Image keyed by file path. Once decoded, an
// image.Image is reused on every render — Fyne's canvas.Image.MinSize calls
// Decode on the file synchronously when i.Image is nil, which inside
// widget.List's SetItemHeight cascade can fire dozens of times per chat
// open. Pre-decoded images bypass that path entirely (canvas.Image checks
// `i.Image == nil` and skips Refresh when not nil).
//
// Decoded images are much larger than their compressed files, so the cache is
// bounded by estimated decoded bytes and evicts least-recently-used entries.
var (
	imgCacheMu       sync.Mutex
	imgCache         = make(map[string]*list.Element)
	imgCacheLRU      = list.New()
	imgCacheBytes    int64
	imgCacheMaxBytes = defaultImageCacheMaxBytes
)

// CachedImage decodes path on first call and returns the cached
// image.Image on subsequent calls. Returns nil on error so callers can
// fall back to canvas.NewImageFromFile (which works correctly for tiny
// images even with the MinSize-decode path).
func CachedImage(path string) image.Image {
	if path == "" {
		return nil
	}
	imgCacheMu.Lock()
	if elem, ok := imgCache[path]; ok {
		imgCacheLRU.MoveToFront(elem)
		img := elem.Value.(*cachedImageEntry).img
		imgCacheMu.Unlock()
		return img
	}
	imgCacheMu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil
	}

	bytes := imageBytes(img)
	imgCacheMu.Lock()
	defer imgCacheMu.Unlock()

	// Another goroutine may have decoded and cached the same path while this
	// caller was doing disk IO. Prefer the existing entry so all readers share
	// one object.
	if elem, ok := imgCache[path]; ok {
		imgCacheLRU.MoveToFront(elem)
		return elem.Value.(*cachedImageEntry).img
	}

	if bytes > imgCacheMaxBytes {
		return img
	}

	elem := imgCacheLRU.PushFront(&cachedImageEntry{
		path:  path,
		img:   img,
		bytes: bytes,
	})
	imgCache[path] = elem
	imgCacheBytes += bytes
	evictImagesLocked()

	return img
}

func evictImagesLocked() {
	for imgCacheBytes > imgCacheMaxBytes {
		elem := imgCacheLRU.Back()
		if elem == nil {
			imgCacheBytes = 0
			return
		}
		entry := elem.Value.(*cachedImageEntry)
		delete(imgCache, entry.path)
		imgCacheLRU.Remove(elem)
		imgCacheBytes -= entry.bytes
		if imgCacheBytes < 0 {
			imgCacheBytes = 0
		}
	}
}

func imageBytes(img image.Image) int64 {
	switch m := img.(type) {
	case *image.Alpha:
		return int64(len(m.Pix))
	case *image.Alpha16:
		return int64(len(m.Pix))
	case *image.CMYK:
		return int64(len(m.Pix))
	case *image.Gray:
		return int64(len(m.Pix))
	case *image.Gray16:
		return int64(len(m.Pix))
	case *image.NRGBA:
		return int64(len(m.Pix))
	case *image.NRGBA64:
		return int64(len(m.Pix))
	case *image.NYCbCrA:
		return int64(len(m.Y)) + int64(len(m.Cb)) + int64(len(m.Cr)) + int64(len(m.A))
	case *image.Paletted:
		return int64(len(m.Pix)) + int64(len(m.Palette))*4
	case *image.RGBA:
		return int64(len(m.Pix))
	case *image.RGBA64:
		return int64(len(m.Pix))
	case *image.YCbCr:
		return int64(len(m.Y)) + int64(len(m.Cb)) + int64(len(m.Cr))
	default:
		b := img.Bounds()
		w, h := int64(b.Dx()), int64(b.Dy())
		if w <= 0 || h <= 0 {
			return 0
		}
		if w > math.MaxInt64/h {
			return math.MaxInt64
		}
		pixels := w * h
		if pixels > math.MaxInt64/4 {
			return math.MaxInt64
		}
		return pixels * 4
	}
}

// renderableImage(path) decodes and caches whatever RenderablePath returns
// for the given media path. Convenience wrapper for callers that always
// want a cache-backed canvas-ready image.
func renderableImage(path string) image.Image {
	return CachedImage(RenderablePath(path))
}
