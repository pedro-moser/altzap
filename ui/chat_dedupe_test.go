package ui

import (
	"testing"

	"altzap/client"

	"go.mau.fi/whatsmeow/types"
)

func mkPN(phone string) types.JID {
	jid, _ := types.ParseJID(phone + "@s.whatsapp.net")
	return jid
}

func mkLID(hash string) types.JID {
	jid, _ := types.ParseJID(hash + "@lid")
	return jid
}

func TestDedupeLIDPNTwins_KeepsNewerTwin_LIDWins(t *testing.T) {
	chats := []client.Chat{
		{JID: mkPN("5511999"), DisplayName: "Miguel", LastMessageTime: 100},
		{JID: mkLID("abc123"), DisplayName: "Miguel", LastMessageTime: 200},
	}
	resolve := func(jid types.JID) string {
		if jid.Server == types.HiddenUserServer && jid.User == "abc123" {
			return "5511999"
		}
		return ""
	}

	got, _ := dedupeLIDPNTwins(chats, resolve)
	if len(got) != 1 {
		t.Fatalf("want 1 chat, got %d", len(got))
	}
	if got[0].JID.Server != types.HiddenUserServer {
		t.Fatalf("expected LID twin (more recent) to win, got %s", got[0].JID.String())
	}
}

func TestDedupeLIDPNTwins_KeepsNewerTwin_PNWins(t *testing.T) {
	chats := []client.Chat{
		{JID: mkPN("5511999"), DisplayName: "Miguel", LastMessageTime: 200},
		{JID: mkLID("abc123"), DisplayName: "Miguel", LastMessageTime: 100},
	}
	resolve := func(jid types.JID) string {
		if jid.Server == types.HiddenUserServer && jid.User == "abc123" {
			return "5511999"
		}
		return ""
	}

	got, _ := dedupeLIDPNTwins(chats, resolve)
	if len(got) != 1 {
		t.Fatalf("want 1 chat, got %d", len(got))
	}
	if got[0].JID.Server != types.DefaultUserServer {
		t.Fatalf("expected PN twin (more recent) to win, got %s", got[0].JID.String())
	}
}

func TestDedupeLIDPNTwins_LIDWithoutMappingPassesThrough(t *testing.T) {
	chats := []client.Chat{
		{JID: mkPN("5511999"), DisplayName: "Miguel", LastMessageTime: 200},
		{JID: mkLID("unknown"), DisplayName: "abc999", LastMessageTime: 50},
	}
	resolve := func(jid types.JID) string { return "" } // no LID mapping anywhere

	got, _ := dedupeLIDPNTwins(chats, resolve)
	if len(got) != 2 {
		t.Fatalf("want both chats kept (no mapping = no twin), got %d", len(got))
	}
}

func TestDedupeLIDPNTwins_PreservesOrder(t *testing.T) {
	chats := []client.Chat{
		{JID: mkPN("5511A"), DisplayName: "A", LastMessageTime: 300},
		{JID: mkPN("5511B"), DisplayName: "B", LastMessageTime: 200},
		{JID: mkLID("hashB"), DisplayName: "B", LastMessageTime: 250},
		{JID: mkPN("5511C"), DisplayName: "C", LastMessageTime: 100},
	}
	resolve := func(jid types.JID) string {
		if jid.Server == types.HiddenUserServer && jid.User == "hashB" {
			return "5511B"
		}
		return ""
	}

	got, _ := dedupeLIDPNTwins(chats, resolve)
	if len(got) != 3 {
		t.Fatalf("want 3 chats (B's PN dropped, LID kept), got %d", len(got))
	}
	want := []string{"A", "B", "C"}
	for i, c := range got {
		if c.DisplayName != want[i] {
			t.Fatalf("position %d: want %q, got %q (full order: %v)", i, want[i], c.DisplayName, got)
		}
	}
}

func TestDedupeLIDPNTwins_GroupsAndOthersUntouched(t *testing.T) {
	groupJID, _ := types.ParseJID("120363999999999999@g.us")
	chats := []client.Chat{
		{JID: groupJID, DisplayName: "Group", IsGroup: true, LastMessageTime: 200},
		{JID: mkPN("5511999"), DisplayName: "Miguel", LastMessageTime: 100},
	}
	resolve := func(jid types.JID) string { return "" }

	got, _ := dedupeLIDPNTwins(chats, resolve)
	if len(got) != 2 {
		t.Fatalf("groups should pass through; want 2, got %d", len(got))
	}
}

func TestDedupeLIDPNTwins_SiblingMap(t *testing.T) {
	chats := []client.Chat{
		{JID: mkPN("5511999"), DisplayName: "Miguel", LastMessageTime: 100},
		{JID: mkLID("abc123"), DisplayName: "Miguel", LastMessageTime: 200},
	}
	resolve := func(jid types.JID) string {
		if jid.User == "abc123" {
			return "5511999"
		}
		return ""
	}

	_, siblings := dedupeLIDPNTwins(chats, resolve)
	pnJID := mkPN("5511999").String()
	lidJID := mkLID("abc123").String()

	if sibs, ok := siblings[pnJID]; !ok || len(sibs) != 1 || sibs[0] != lidJID {
		t.Fatalf("PN should have LID as sibling, got %v", siblings[pnJID])
	}
	if sibs, ok := siblings[lidJID]; !ok || len(sibs) != 1 || sibs[0] != pnJID {
		t.Fatalf("LID should have PN as sibling, got %v", siblings[lidJID])
	}
}
