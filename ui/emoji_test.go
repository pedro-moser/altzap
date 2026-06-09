package ui

import (
	"testing"
	"unicode/utf8"
)

func TestInsertAtRuneOffset(t *testing.T) {
	cases := []struct {
		name    string
		s, ins  string
		pos     int
		want    string
		wantPos int
	}{
		{"empty text", "", "😀", 0, "😀", 1},
		{"append at end", "oi", "😀", 2, "oi😀", 3},
		{"insert at start", "oi", "😀", 0, "😀oi", 1},
		{"middle of ascii", "abcd", "x", 2, "abxcd", 3},
		{"after accented rune", "ção", "😀", 2, "çã😀o", 3},
		{"between emoji", "👍👍", "x", 1, "👍x👍", 2},
		{"negative pos clamps to start", "abc", "x", -3, "xabc", 1},
		{"pos past end clamps to end", "çã", "x", 99, "çãx", 3},
		{"multi-rune insertion", "ab", "çã", 1, "açãb", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, gotPos := insertAtRuneOffset(tc.s, tc.ins, tc.pos)
			if got != tc.want || gotPos != tc.wantPos {
				t.Fatalf("insertAtRuneOffset(%q, %q, %d) = (%q, %d), want (%q, %d)",
					tc.s, tc.ins, tc.pos, got, gotPos, tc.want, tc.wantPos)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("result %q is invalid UTF-8", got)
			}
		})
	}
}
