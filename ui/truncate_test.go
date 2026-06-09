package ui

import (
	"testing"
	"unicode/utf8"
)

func TestTruncate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"shorter than limit", "oi", 10, "oi"},
		{"exactly at limit", "abcde", 5, "abcde"},
		{"ascii cut", "abcdef", 3, "abc…"},
		{"accent at boundary", "ação", 2, "aç…"},
		{"accents counted as one rune", "ação", 4, "ação"},
		{"emoji at boundary", "ok👍👍", 3, "ok👍…"},
		{"empty", "", 5, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.in, tc.n)
			if got != tc.want {
				t.Fatalf("truncate(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("truncate(%q, %d) produced invalid UTF-8: %q", tc.in, tc.n, got)
			}
		})
	}
}

// TestTruncateNeverProducesInvalidUTF8 sweeps every cut point of a string
// mixing 1–4 byte runes — the old byte-slicing implementation failed this.
func TestTruncateNeverProducesInvalidUTF8(t *testing.T) {
	s := "a çã😀b ção👍 fim"
	for n := 0; n <= utf8.RuneCountInString(s)+1; n++ {
		if got := truncate(s, n); !utf8.ValidString(got) {
			t.Fatalf("truncate(%q, %d) = %q is invalid UTF-8", s, n, got)
		}
	}
}
