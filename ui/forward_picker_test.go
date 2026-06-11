package ui

import (
	"testing"

	"altzap/client"

	"go.mau.fi/whatsmeow/types"
)

func TestMergeForwardCandidates(t *testing.T) {
	group := client.Chat{JID: types.NewJID("123-group", types.GroupServer), DisplayName: "Família", IsGroup: true}
	mariaChatLID := client.Chat{JID: types.NewJID("9988776655", types.HiddenUserServer), DisplayName: "Maria"}
	bobChat := client.Chat{JID: types.NewJID("5511222222222", types.DefaultUserServer), DisplayName: "Bob"}

	mariaContact := client.Contact{JID: types.NewJID("5511111111111", types.DefaultUserServer), Name: "Maria"}
	zoeContact := client.Contact{JID: types.NewJID("5511333333333", types.DefaultUserServer), Name: "Zoé"}
	anaContact := client.Contact{JID: types.NewJID("5511444444444", types.DefaultUserServer), Name: "Ana"}

	phoneFor := func(j types.JID) string {
		if j.Server == types.HiddenUserServer {
			return "5511111111111" // Maria's LID maps to her phone
		}
		if j.Server == types.DefaultUserServer {
			return j.User
		}
		return ""
	}
	nameFor := func(j types.JID) string { return j.User }

	got := mergeForwardCandidates(
		[]client.Chat{group, mariaChatLID, bobChat},
		[]client.Contact{zoeContact, mariaContact, anaContact},
		phoneFor, nameFor,
	)

	wantNames := []string{"Família", "Maria", "Bob", "Ana", "Zoé"}
	if len(got) != len(wantNames) {
		t.Fatalf("got %d candidates (%+v), want %d", len(got), got, len(wantNames))
	}
	for i, want := range wantNames {
		if got[i].Name != want {
			t.Errorf("candidate[%d].Name = %q, want %q (full: %+v)", i, got[i].Name, want, got)
		}
	}

	// Maria the contact must have been deduped against her LID chat by
	// canonical phone — exactly one Maria in the list.
	marias := 0
	for _, c := range got {
		if c.Name == "Maria" {
			marias++
		}
	}
	if marias != 1 {
		t.Errorf("LID/PN twin not deduped: %d Maria entries", marias)
	}

	if got[0].Subtitle != "Group" {
		t.Errorf("group subtitle = %q, want \"Group\"", got[0].Subtitle)
	}
}

func TestFilterForwardCandidates(t *testing.T) {
	all := []forwardCandidate{
		{JID: "1@g.us", Name: "Família", Subtitle: "Group"},
		{JID: "2@s.whatsapp.net", Name: "Maria", Subtitle: "+5511111111111"},
		{JID: "3@s.whatsapp.net", Name: "Bob", Subtitle: "+5511222222222"},
	}

	cases := []struct {
		query string
		want  int
	}{
		{"", 3},
		{"  ", 3},
		{"maria", 1},
		{"MARIA", 1},
		{"famí", 1},
		{"5511", 2},
		{"g.us", 1},
		{"nope", 0},
	}
	for _, tc := range cases {
		if got := filterForwardCandidates(all, tc.query); len(got) != tc.want {
			t.Errorf("filter(%q) returned %d entries, want %d", tc.query, len(got), tc.want)
		}
	}
}
