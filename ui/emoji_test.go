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

func TestRuneOffsetForCursor(t *testing.T) {
	text := "çã\nab\nfim"
	cases := []struct {
		name     string
		row, col int
		want     int
	}{
		{"start", 0, 0, 0},
		{"end of accented line", 0, 2, 2},
		{"start of second line", 1, 0, 3},
		{"middle of second line", 1, 1, 4},
		{"third line", 2, 3, 9},
		{"col overflow clamps to line end", 0, 99, 2},
		{"row overflow clamps to last line", 99, 1, 7},
		{"negative clamps to zero", -1, -1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runeOffsetForCursor(text, tc.row, tc.col); got != tc.want {
				t.Fatalf("runeOffsetForCursor(%q, %d, %d) = %d, want %d",
					text, tc.row, tc.col, got, tc.want)
			}
		})
	}
}

func TestCursorForRuneOffsetRoundTrip(t *testing.T) {
	text := "çã\nab\nfim"
	for off := 0; off <= 9; off++ {
		row, col := cursorForRuneOffset(text, off)
		if back := runeOffsetForCursor(text, row, col); back != off {
			t.Fatalf("offset %d → (%d,%d) → %d: round trip broke", off, row, col, back)
		}
	}
}
