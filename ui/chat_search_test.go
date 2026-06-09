package ui

import (
	"testing"

	"altzap/client"
	"go.mau.fi/whatsmeow/types"
)

func TestFilterChatsByQueryPreservesRecencyOrder(t *testing.T) {
	chats := []client.Chat{
		{JID: types.NewJID("recent", types.DefaultUserServer), DisplayName: "Recent Project", LastMessageTime: 300},
		{JID: types.NewJID("middle", types.DefaultUserServer), DisplayName: "Project Updates", LastMessageTime: 200},
		{JID: types.NewJID("old", types.DefaultUserServer), DisplayName: "Old Project", LastMessageTime: 100},
	}

	got := filterChatsByQuery(chats, "project")
	if len(got) != len(chats) {
		t.Fatalf("got %d chats, want %d", len(got), len(chats))
	}
	for i := range chats {
		if got[i].JID != chats[i].JID {
			t.Fatalf("result %d = %s, want %s", i, got[i].JID, chats[i].JID)
		}
	}
}

func TestFilterChatsByQueryMatchesJIDAndDeduplicates(t *testing.T) {
	jid := types.NewJID("5511999999999", types.DefaultUserServer)
	chats := []client.Chat{
		{JID: jid, DisplayName: "Alice", LastMessageTime: 300},
		{JID: jid, DisplayName: "Alice duplicate", LastMessageTime: 200},
		{JID: types.NewJID("5511888888888", types.DefaultUserServer), DisplayName: "Bob", LastMessageTime: 100},
	}

	got := filterChatsByQuery(chats, "9999")
	if len(got) != 1 {
		t.Fatalf("got %d chats, want 1", len(got))
	}
	if got[0].JID != jid {
		t.Fatalf("got %s, want %s", got[0].JID, jid)
	}
}
