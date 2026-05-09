package client

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/image/vp8"
	"golang.org/x/image/vp8l"
)

// renderableInflight dedupes concurrent transcode requests on the same .webp
// (UI rebuilds and post-download eager calls can race).
var (
	renderableMu       sync.Mutex
	renderableInflight = make(map[string]chan struct{})
)

// RenderablePath returns a path that Fyne's canvas.Image can decode reliably.
//
// WhatsApp ships stickers as VP8X webp containers — many of them either set
// the alpha bit on a VP8L body (which golang.org/x/image/webp rejects per a
// strict spec read) or are animated via ANMF chunks (unsupported there).
// Both surface as `webp: invalid format` and flood Fyne's renderer with
// errors on every layout pass.
//
// We sidestep both by transcoding the offending webp to a sibling .png on
// first encounter using the lower-level vp8/vp8l packages we already pull in
// transitively. Animation collapses to the first frame, which matches the
// "preview" expectation for stickers in a chat list.
//
// For non-webp paths or webps the stdlib already handles, returns p
// unchanged. The .png is cached next to the original.
func RenderablePath(p string) string {
	if !strings.HasSuffix(strings.ToLower(p), ".webp") {
		return p
	}
	pngPath := p + ".png"
	if fileExists(pngPath) {
		return pngPath
	}

	renderableMu.Lock()
	if ch, ok := renderableInflight[p]; ok {
		renderableMu.Unlock()
		<-ch
		if fileExists(pngPath) {
			return pngPath
		}
		return p
	}
	done := make(chan struct{})
	renderableInflight[p] = done
	renderableMu.Unlock()
	defer func() {
		renderableMu.Lock()
		delete(renderableInflight, p)
		close(done)
		renderableMu.Unlock()
	}()

	if err := transcodeWebPToPNG(p, pngPath); err != nil {
		log.Printf("webp→png %s: %v", p, err)
		return p
	}
	return pngPath
}

func transcodeWebPToPNG(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	img, err := decodeWebP(data)
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("encode: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// decodeWebP first delegates to the registered stdlib decoder (handles plain
// VP8/VP8L stills and even some VP8X variants). On rejection, falls back to
// our own RIFF chunk walk which covers the two cases WhatsApp routinely sends
// that the stdlib refuses.
func decodeWebP(data []byte) (image.Image, error) {
	if img, _, err := image.Decode(bytes.NewReader(data)); err == nil {
		return img, nil
	}
	return decodeWebPFallback(data)
}

func decodeWebPFallback(data []byte) (image.Image, error) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return nil, errors.New("not a RIFF/WEBP file")
	}
	chunks, err := iterRIFFChunks(data[12:])
	if err != nil {
		return nil, fmt.Errorf("riff: %w", err)
	}

	// Look for a top-level VP8L (covers VP8X→VP8L stills the stdlib rejects
	// when the alpha bit is set on the VP8X header but VP8L already carries
	// alpha intrinsically).
	for _, c := range chunks {
		if c.id == "VP8L" {
			return vp8l.Decode(bytes.NewReader(c.body))
		}
	}

	// Animated webp: pull the first frame from the first ANMF chunk.
	for _, c := range chunks {
		if c.id == "ANMF" {
			return decodeANMFFirstFrame(c.body)
		}
	}

	// Last resort: top-level VP8 (lossy). Alpha is dropped.
	for _, c := range chunks {
		if c.id == "VP8 " {
			return decodeVP8Frame(c.body)
		}
	}
	return nil, errors.New("no VP8/VP8L/ANMF chunk found")
}

// ANMF chunk body: 16-byte header (frame x/y/width/height/duration as 3-byte
// LE values + 1 flags byte) followed by an optional ALPH chunk and a single
// VP8 or VP8L bitstream chunk in standard RIFF chunk layout.
func decodeANMFFirstFrame(body []byte) (image.Image, error) {
	if len(body) <= 16 {
		return nil, errors.New("anmf: truncated")
	}
	inner, err := iterRIFFChunks(body[16:])
	if err != nil {
		return nil, fmt.Errorf("anmf inner: %w", err)
	}
	for _, c := range inner {
		if c.id == "VP8L" {
			return vp8l.Decode(bytes.NewReader(c.body))
		}
	}
	for _, c := range inner {
		if c.id == "VP8 " {
			return decodeVP8Frame(c.body)
		}
	}
	return nil, errors.New("anmf: no VP8/VP8L sub-chunk")
}

func decodeVP8Frame(body []byte) (image.Image, error) {
	d := vp8.NewDecoder()
	d.Init(bytes.NewReader(body), len(body))
	if _, err := d.DecodeFrameHeader(); err != nil {
		return nil, fmt.Errorf("vp8 header: %w", err)
	}
	m, err := d.DecodeFrame()
	if err != nil {
		return nil, fmt.Errorf("vp8 frame: %w", err)
	}
	return m, nil
}

// PreTranscodeWebPMedia walks MediaDir() and transcodes every .webp without a
// sibling .png cache, with bounded concurrency. Idempotent. Designed to be
// called once at startup in a goroutine — UI never blocks on this.
func PreTranscodeWebPMedia() {
	const workers = 4
	jobs := make(chan string, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				_ = RenderablePath(p)
			}
		}()
	}

	count := 0
	_ = filepath.Walk(MediaDir(), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(p), ".webp") {
			return nil
		}
		if fileExists(p + ".png") {
			return nil
		}
		jobs <- p
		count++
		return nil
	})
	close(jobs)
	wg.Wait()
	if count > 0 {
		log.Printf("PreTranscodeWebPMedia: prepared %d webp→png caches", count)
	}
}

type riffChunk struct {
	id   string
	body []byte
}

// iterRIFFChunks walks RIFF-style chunks: 4-byte FourCC, 4-byte LE size,
// body, optional 1 byte of pad to keep 16-bit alignment.
func iterRIFFChunks(data []byte) ([]riffChunk, error) {
	var out []riffChunk
	r := bytes.NewReader(data)
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if err == io.EOF {
				return out, nil
			}
			return nil, err
		}
		size := binary.LittleEndian.Uint32(hdr[4:])
		body := make([]byte, size)
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, err
		}
		if size%2 == 1 {
			_, _ = r.ReadByte()
		}
		out = append(out, riffChunk{id: string(hdr[0:4]), body: body})
	}
}
