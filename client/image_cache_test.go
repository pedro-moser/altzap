package client

import (
	"container/list"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestCachedImageEvictsLeastRecentlyUsed(t *testing.T) {
	withImageCacheLimit(t, 900)
	dir := t.TempDir()
	a := writeCacheTestPNG(t, dir, "a.png")
	b := writeCacheTestPNG(t, dir, "b.png")
	c := writeCacheTestPNG(t, dir, "c.png")

	if CachedImage(a) == nil || CachedImage(b) == nil {
		t.Fatal("expected initial images to decode")
	}
	if !cacheHasImage(a) || !cacheHasImage(b) {
		t.Fatal("expected both images cached before exceeding limit")
	}

	// Refresh a so b is the least-recently-used entry.
	if CachedImage(a) == nil {
		t.Fatal("expected cached image hit")
	}
	if CachedImage(c) == nil {
		t.Fatal("expected third image to decode")
	}

	if !cacheHasImage(a) {
		t.Fatal("recently used image was evicted")
	}
	if cacheHasImage(b) {
		t.Fatal("least-recently-used image remained cached")
	}
	if !cacheHasImage(c) {
		t.Fatal("new image was not cached")
	}
	if _, bytes := imageCacheState(); bytes > imgCacheMaxBytes {
		t.Fatalf("cache exceeds limit: got %d, max %d", bytes, imgCacheMaxBytes)
	}
}

func TestCachedImageSkipsOversizedEntries(t *testing.T) {
	withImageCacheLimit(t, 399)
	path := writeCacheTestPNG(t, t.TempDir(), "large.png")

	if CachedImage(path) == nil {
		t.Fatal("expected image to decode even when it is not cached")
	}
	if cacheHasImage(path) {
		t.Fatal("oversized image should not be cached")
	}
	if count, bytes := imageCacheState(); count != 0 || bytes != 0 {
		t.Fatalf("cache should be empty, got count=%d bytes=%d", count, bytes)
	}
}

func withImageCacheLimit(t *testing.T, maxBytes int64) {
	t.Helper()
	oldMax := imgCacheMaxBytes
	resetImageCacheForTest(maxBytes)
	t.Cleanup(func() {
		resetImageCacheForTest(oldMax)
	})
}

func resetImageCacheForTest(maxBytes int64) {
	imgCacheMu.Lock()
	defer imgCacheMu.Unlock()
	imgCache = make(map[string]*list.Element)
	imgCacheLRU = list.New()
	imgCacheBytes = 0
	imgCacheMaxBytes = maxBytes
}

func writeCacheTestPNG(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	img := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 0x80, A: 0xff})
		}
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

func cacheHasImage(path string) bool {
	imgCacheMu.Lock()
	defer imgCacheMu.Unlock()
	_, ok := imgCache[path]
	return ok
}

func imageCacheState() (int, int64) {
	imgCacheMu.Lock()
	defer imgCacheMu.Unlock()
	return len(imgCache), imgCacheBytes
}
