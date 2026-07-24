package ui

import (
	"io"
	"strings"
	"testing"
)

func TestCapWriter_TruncatesButReportsFullWrite(t *testing.T) {
	w := &capWriter{max: 8}
	n, err := w.Write([]byte("0123456789abcdef"))
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	// Reporting the truncated length would make io.Copy fail with
	// ErrShortWrite, which would then be misreported as a launcher failure.
	if n != 16 {
		t.Errorf("Write reported n=%d; want 16 (the full input length)", n)
	}
	if got := w.String(); got != "01234567" {
		t.Errorf("String() = %q; want %q", got, "01234567")
	}
}

func TestCapWriter_SurvivesIOCopyPastTheCap(t *testing.T) {
	w := &capWriter{max: 16}
	if _, err := io.Copy(w, strings.NewReader(strings.Repeat("x", 4096))); err != nil {
		t.Fatalf("io.Copy into capWriter: %v", err)
	}
	if got := w.String(); got != strings.Repeat("x", 16) {
		t.Errorf("String() = %q; want 16 x's", got)
	}
}

func TestCapWriter_TrimsSurroundingWhitespace(t *testing.T) {
	w := &capWriter{max: 4096}
	_, _ = w.Write([]byte("  sioyek: error while loading shared libraries\n"))
	if got, want := w.String(), "sioyek: error while loading shared libraries"; got != want {
		t.Errorf("String() = %q; want %q", got, want)
	}
}
