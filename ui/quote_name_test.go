package ui

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestIsHumanName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"236725491761089", false},   // raw LID hash
		{"5511987654321", false},     // bare phone
		{"+55 11 98765-4321", false}, // formatted phone
		{"(11) 98765.4321", false},   // punctuation-only phone
		{"Maria", true},
		{"João 2", true}, // digits mixed with letters
		{"You", true},
		{"🦆", true}, // emoji-only push names exist
	}
	for _, tc := range cases {
		if got := isHumanName(tc.in); got != tc.want {
			t.Errorf("isHumanName(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestQuotedSenderDisplay(t *testing.T) {
	const lidJID = "236725491761089@lid"
	const lidHash = "236725491761089"

	lookupReturning := func(name string) func(types.JID) string {
		return func(types.JID) string { return name }
	}

	cases := []struct {
		name   string
		stored string
		jidStr string
		lookup func(types.JID) string
		want   string
	}{
		{
			name:   "human stored name wins without lookup",
			stored: "Maria",
			jidStr: lidJID,
			lookup: lookupReturning("Other"),
			want:   "Maria",
		},
		{
			name:   "You is kept for own quotes",
			stored: "You",
			jidStr: "5511999999999@s.whatsapp.net",
			lookup: lookupReturning("Pedro"),
			want:   "You",
		},
		{
			name:   "frozen LID hash re-resolves to contact name",
			stored: lidHash,
			jidStr: lidJID,
			lookup: lookupReturning("Maria"),
			want:   "Maria",
		},
		{
			name:   "empty stored name re-resolves",
			stored: "",
			jidStr: lidJID,
			lookup: lookupReturning("Maria"),
			want:   "Maria",
		},
		{
			name:   "hash upgrades to phone when mapping landed",
			stored: lidHash,
			jidStr: lidJID,
			lookup: lookupReturning("5511987654321"),
			want:   "5511987654321",
		},
		{
			name:   "stored phone not downgraded to hash",
			stored: "5511987654321",
			jidStr: lidJID,
			lookup: lookupReturning(lidHash),
			want:   "5511987654321",
		},
		{
			name:   "no JID keeps stored value",
			stored: lidHash,
			jidStr: "",
			lookup: lookupReturning("Maria"),
			want:   lidHash,
		},
		{
			name:   "nil lookup keeps stored value",
			stored: lidHash,
			jidStr: lidJID,
			lookup: nil,
			want:   lidHash,
		},
		{
			name:   "unparseable JID keeps stored value",
			stored: lidHash,
			jidStr: "a.b.c@lid", // ParseJID rejects multi-dot users
			lookup: lookupReturning("Maria"),
			want:   lidHash,
		},
		{
			name:   "unresolvable LID with nothing stored stays empty (caller shows Someone)",
			stored: "",
			jidStr: lidJID,
			lookup: lookupReturning(lidHash), // lookup falls back to the hash itself
			want:   "",
		},
		{
			name:   "PN JID with nothing stored shows the phone",
			stored: "",
			jidStr: "5511987654321@s.whatsapp.net",
			lookup: lookupReturning("5511987654321"),
			want:   "5511987654321",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := quotedSenderDisplay(tc.stored, tc.jidStr, tc.lookup); got != tc.want {
				t.Errorf("quotedSenderDisplay(%q, %q) = %q, want %q", tc.stored, tc.jidStr, got, tc.want)
			}
		})
	}
}
