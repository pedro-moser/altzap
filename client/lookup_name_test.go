package client

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func newTestClient() *WhatsAppClient {
	return &WhatsAppClient{
		ContactCache: make(map[string]Contact),
		chatRegistry: make(map[string]string),
		groupCache:   make(map[string]string),
		lidPNCache:   make(map[string]types.JID),
	}
}

func TestLookupName_GroupCacheWins(t *testing.T) {
	w := newTestClient()
	jid, _ := types.ParseJID("120363999999999999@g.us")
	w.groupCache[jid.String()] = "My Group"
	w.chatRegistry[jid.String()] = "stale"

	if got := w.LookupName(jid); got != "My Group" {
		t.Fatalf("want My Group, got %q", got)
	}
}

func TestLookupName_ContactCacheBeforeChatRegistry(t *testing.T) {
	w := newTestClient()
	jid, _ := types.ParseJID("5511999999999@s.whatsapp.net")
	w.ContactCache[jid.String()] = Contact{JID: jid, Name: "Alice"}
	w.chatRegistry[jid.String()] = "WrongFallback"

	if got := w.LookupName(jid); got != "Alice" {
		t.Fatalf("want Alice, got %q", got)
	}
}

func TestDisplayNameFromContactInfo_UsesFirstNameBeforePushName(t *testing.T) {
	info := types.ContactInfo{
		FirstName: "Saved First",
		PushName:  "Public Push",
	}

	if got := displayNameFromContactInfo(info); got != "Saved First" {
		t.Fatalf("want saved first name, got %q", got)
	}
}

func TestRememberPushNameDoesNotOverwriteSavedContactName(t *testing.T) {
	w := newTestClient()
	jid, _ := types.ParseJID("5511999999999@s.whatsapp.net")
	w.ContactCache[jid.String()] = Contact{JID: jid, Name: "Saved Name"}

	w.rememberPushName(jid, "Public Push")

	if got := w.LookupName(jid); got != "Saved Name" {
		t.Fatalf("want saved contact name to win, got %q", got)
	}
}

func TestLookupName_ChatRegistryWhenContactCacheEmptyName(t *testing.T) {
	w := newTestClient()
	jid, _ := types.ParseJID("5511999999999@s.whatsapp.net")
	w.ContactCache[jid.String()] = Contact{JID: jid, Name: ""}
	w.chatRegistry[jid.String()] = "Bob"

	if got := w.LookupName(jid); got != "Bob" {
		t.Fatalf("want Bob, got %q", got)
	}
}

func TestLookupName_FallbackToJIDUser(t *testing.T) {
	w := newTestClient()
	jid, _ := types.ParseJID("5511999999999@s.whatsapp.net")

	if got := w.LookupName(jid); got != "5511999999999" {
		t.Fatalf("want phone, got %q", got)
	}
}

func TestLookupName_LIDPNCacheHit(t *testing.T) {
	// Simulate the LID→PN→ContactCache resolution: when a LID has a
	// mapped phone-number JID and that PN is in the contact cache with a
	// name, LookupName should return the PN's name. This guards the bug
	// where LookupName surfaced a raw LID hash for chats that had a
	// resolvable phone-number contact.
	w := newTestClient()
	pnJID, _ := types.ParseJID("5511999999999@s.whatsapp.net")
	w.ContactCache[pnJID.String()] = Contact{JID: pnJID, Name: "Carla"}

	lidJID, _ := types.ParseJID("123456789012345@lid")
	w.lidPNCache = map[string]types.JID{
		lidJID.String(): pnJID,
	}

	if got := w.LookupName(lidJID); got != "Carla" {
		t.Fatalf("want Carla via LID->PN, got %q", got)
	}
}

func TestLookupName_LIDFallsBackToPhoneWhenPNHasNoName(t *testing.T) {
	w := newTestClient()
	pnJID, _ := types.ParseJID("5511999999999@s.whatsapp.net")
	w.ContactCache[pnJID.String()] = Contact{JID: pnJID, Name: ""}

	lidJID, _ := types.ParseJID("123456789012345@lid")
	w.lidPNCache = map[string]types.JID{lidJID.String(): pnJID}

	if got := w.LookupName(lidJID); got != "5511999999999" {
		t.Fatalf("want phone fallback, got %q", got)
	}
}

func TestLookupName_LIDFallsBackToHashWithoutMapping(t *testing.T) {
	w := newTestClient()
	lidJID, _ := types.ParseJID("123456789012345@lid")

	if got := w.LookupName(lidJID); got != "123456789012345" {
		t.Fatalf("want LID hash fallback, got %q", got)
	}
}
