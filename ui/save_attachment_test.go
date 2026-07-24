package ui

import (
	"testing"
	"time"
)

func testTime() time.Time {
	return time.Date(2026, 7, 24, 8, 59, 36, 0, time.UTC)
}

func TestSuggestedSaveName_HonorsDeclaredFileName(t *testing.T) {
	msg := &Message{
		MediaType: "document",
		MediaPath: "/data/media/chat/3A53.pdf",
		Mimetype:  "application/pdf",
		FileName:  "Curso LGPD na Prática Hospitalar — Slides.pdf",
		Timestamp: testTime(),
	}
	if got, want := suggestedSaveName(msg), "Curso LGPD na Prática Hospitalar — Slides.pdf"; got != want {
		t.Fatalf("suggestedSaveName = %q; want %q", got, want)
	}
}

// A .docx lands on disk as ".bin" (extForMime has no mapping for it), so the
// cached path's extension must never win over the sender's declared name.
func TestSuggestedSaveName_DeclaredNameBeatsBinCachePath(t *testing.T) {
	msg := &Message{
		MediaType: "document",
		MediaPath: "/data/media/chat/4AD3.bin",
		Mimetype:  "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		FileName:  "Contrato Aluguel Osmair.docx",
		Timestamp: testTime(),
	}
	if got, want := suggestedSaveName(msg), "Contrato Aluguel Osmair.docx"; got != want {
		t.Fatalf("suggestedSaveName = %q; want %q", got, want)
	}
}

func TestSuggestedSaveName_AppendsExtensionWhenDeclaredNameHasNone(t *testing.T) {
	msg := &Message{
		MediaType: "document",
		MediaPath: "/data/media/chat/3A53.pdf",
		Mimetype:  "application/pdf",
		FileName:  "laudo-sem-extensao",
		Timestamp: testTime(),
	}
	if got, want := suggestedSaveName(msg), "laudo-sem-extensao.pdf"; got != want {
		t.Fatalf("suggestedSaveName = %q; want %q", got, want)
	}
}

// FileName arrives over the network. A crafted one must collapse to a bare
// basename so it can never steer the write out of the chosen directory.
func TestSuggestedSaveName_StripsPathTraversal(t *testing.T) {
	// "passwd" survives as a basename but carries no extension, so the
	// mimetype's ".pdf" is appended — same rule as any other bare name.
	cases := map[string]string{
		"../../.bashrc":          ".bashrc",
		"/etc/passwd":            "passwd.pdf",
		"..\\..\\windows\\a.exe": "a.exe",
		"sub/dir/report.pdf":     "report.pdf",
	}
	for in, want := range cases {
		msg := &Message{
			MediaType: "document",
			MediaPath: "/data/media/chat/3A53.pdf",
			Mimetype:  "application/pdf",
			FileName:  in,
			Timestamp: testTime(),
		}
		if got := suggestedSaveName(msg); got != want {
			t.Errorf("suggestedSaveName(FileName=%q) = %q; want %q", in, got, want)
		}
	}
}

func TestSuggestedSaveName_GeneratesNameForUnnamedMedia(t *testing.T) {
	cases := []struct {
		mediaType string
		path      string
		mime      string
		want      string
	}{
		{"image", "/data/media/chat/3A01.jpg", "image/jpeg", "IMG-20260724-085936.jpg"},
		{"video", "/data/media/chat/3A00.mp4", "video/mp4", "VID-20260724-085936.mp4"},
		{"voice", "/data/media/chat/3A03.ogg", "audio/ogg", "PTT-20260724-085936.ogg"},
		{"audio", "/data/media/chat/3A04.mp3", "audio/mpeg", "AUD-20260724-085936.mp3"},
		{"sticker", "/data/media/chat/3A04.webp", "image/webp", "STK-20260724-085936.webp"},
	}
	for _, tc := range cases {
		msg := &Message{
			MediaType: tc.mediaType,
			MediaPath: tc.path,
			Mimetype:  tc.mime,
			Timestamp: testTime(),
		}
		if got := suggestedSaveName(msg); got != tc.want {
			t.Errorf("suggestedSaveName(%s) = %q; want %q", tc.mediaType, got, tc.want)
		}
	}
}

// No declared name and a ".bin" cache path: fall back to the mimetype rather
// than handing the user a ".bin" the OS cannot route.
func TestSuggestedSaveName_RederivesExtensionFromMimeWhenCacheIsBin(t *testing.T) {
	msg := &Message{
		MediaType: "document",
		MediaPath: "/data/media/chat/3EB0.bin",
		Mimetype:  "application/pdf",
		Timestamp: testTime(),
	}
	if got, want := suggestedSaveName(msg), "FILE-20260724-085936.pdf"; got != want {
		t.Fatalf("suggestedSaveName = %q; want %q", got, want)
	}
}

// Nothing usable anywhere: still produce a non-empty, extension-less name
// instead of an empty string the save dialog would reject.
func TestSuggestedSaveName_UnknownEverything(t *testing.T) {
	msg := &Message{
		MediaType: "document",
		MediaPath: "/data/media/chat/3EB0.bin",
		Mimetype:  "application/x-whatever",
		Timestamp: testTime(),
	}
	if got, want := suggestedSaveName(msg), "FILE-20260724-085936"; got != want {
		t.Fatalf("suggestedSaveName = %q; want %q", got, want)
	}
}

func TestSuggestedSaveName_NilMessage(t *testing.T) {
	if got := suggestedSaveName(nil); got != "" {
		t.Fatalf("suggestedSaveName(nil) = %q; want empty", got)
	}
}

func TestSanitizeSaveName_DropsControlCharsAndBlankResults(t *testing.T) {
	if got := sanitizeSaveName("re\x00port\n.pdf"); got != "report.pdf" {
		t.Errorf("sanitizeSaveName control chars = %q; want %q", got, "report.pdf")
	}
	for _, in := range []string{"", "   ", "..", "/", "././"} {
		if got := sanitizeSaveName(in); got != "" {
			t.Errorf("sanitizeSaveName(%q) = %q; want empty", in, got)
		}
	}
}
