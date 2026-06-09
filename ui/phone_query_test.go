package ui

import "testing"

func TestExtractPhoneDigits(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"+55 11 99999-8888", "5511999998888"},
		{"5511999998888", "5511999998888"},
		{"(11) 9 9999-8888", "11999998888"},
		{"55.11.99999.8888", "5511999998888"},
		{"  +49 170 1234567 ", "491701234567"},
		{"12345678", "12345678"},
		{"1234567", ""},     // too short
		{"joão", ""},        // letters
		{"5511 abc 99", ""}, // mixed letters
		{"", ""},            // empty
		{"+", ""},           // separators only
		{"99999999x", ""},   // trailing letter
	}
	for _, tc := range cases {
		if got := extractPhoneDigits(tc.in); got != tc.want {
			t.Errorf("extractPhoneDigits(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
