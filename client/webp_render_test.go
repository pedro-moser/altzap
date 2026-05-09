package client

import (
	"errors"
	"image"
	_ "image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestRenderablePathTranscodesProblemWebP picks a real .webp under ../media that
// the stdlib decoder (golang.org/x/image/webp) rejects — typically VP8X+VP8L
// stills WhatsApp ships with the alpha bit set, or animated stickers using
// ANMF chunks. Asserts RenderablePath produces a sibling .png that decodes.
//
// Skipped when no such fixture exists; we don't ship synthetic webp bytes
// because they wouldn't exercise the same decoder rejection paths.
func TestRenderablePathTranscodesProblemWebP(t *testing.T) {
	mediaRoot := MediaDir()
	problem := findProblemWebP(mediaRoot)
	if problem == "" {
		t.Skip("no stdlib-rejected .webp under ../media — nothing to verify")
	}

	cached := problem + ".png"
	preexisted := fileExists(cached)
	t.Cleanup(func() {
		if !preexisted {
			_ = os.Remove(cached)
		}
	})

	out := RenderablePath(problem)
	if out == problem {
		t.Fatalf("RenderablePath(%q) returned the input unchanged; expected a transcoded sibling .png", problem)
	}
	if out != cached {
		t.Fatalf("RenderablePath(%q) = %q; want %q", problem, out, cached)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("open transcoded %q: %v", out, err)
	}
	defer f.Close()
	if _, _, err := image.Decode(f); err != nil {
		t.Fatalf("decode transcoded %q: %v — file is not a valid image", out, err)
	}
}

// findProblemWebP scans root for a .webp file the stdlib rejects, returning
// the first match (empty string if none).
func findProblemWebP(root string) string {
	var found string
	stop := errors.New("stop")
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if filepath.Ext(p) != ".webp" {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		_, _, decodeErr := image.Decode(f)
		_ = f.Close()
		if decodeErr != nil {
			found = p
			return stop
		}
		return nil
	})
	return found
}
