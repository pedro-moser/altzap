package client

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
)

func newTestClient() *WhatsAppClient {
	return &WhatsAppClient{
		ContactCache:  make(map[string]Contact),
		pushNameCache: make(map[string]string),
		chatRegistry:  make(map[string]string),
		groupCache:    make(map[string]string),
		lidPNCache:    make(map[string]types.JID),
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

func TestDisplayNameFromContactInfo_IgnoresRedactedPhone(t *testing.T) {
	// RedactedPhone is a masked phone, not a name. Using it as Name makes
	// a @lid contact's cache entry non-empty and short-circuits LID→PN
	// resolution, surfacing "+55 51 9****-**21" instead of the real name.
	info := types.ContactInfo{RedactedPhone: "+55 51 9****-**21"}
	if got := displayNameFromContactInfo(info); got != "" {
		t.Fatalf("redacted phone must not be used as a name, got %q", got)
	}
}

func TestDisplayNameFromContactInfo_OnlyUsesSavedNames(t *testing.T) {
	// PushName and BusinessName are network-provided names tracked in
	// pushNameCache, not saved address-book names. displayNameFromContactInfo
	// (which feeds the saved-name ContactCache) must ignore them, otherwise a
	// DB push name would outrank a fresher live push name and freeze renames.
	if got := displayNameFromContactInfo(types.ContactInfo{PushName: "Public Push"}); got != "" {
		t.Fatalf("push name is not a saved name, got %q", got)
	}
	if got := displayNameFromContactInfo(types.ContactInfo{BusinessName: "Acme LLC"}); got != "" {
		t.Fatalf("business name is not a saved name, got %q", got)
	}
	if got := displayNameFromContactInfo(types.ContactInfo{FullName: "Full Name", FirstName: "First"}); got != "Full Name" {
		t.Fatalf("want FullName, got %q", got)
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

func TestSeedChatRegistry_SkipsGroups(t *testing.T) {
	w := newTestClient()
	groupJID := "120363999999999999@g.us"
	w.seedChatRegistry(groupJID, true, "Alice")
	if name, ok := w.chatRegistry[groupJID]; ok {
		t.Fatalf("group must not be seeded into chatRegistry, got %q", name)
	}
}

func TestSeedChatRegistry_SeedsDirectChat(t *testing.T) {
	w := newTestClient()
	pnJID := "5511999999999@s.whatsapp.net"
	w.seedChatRegistry(pnJID, false, "Alice")
	if w.chatRegistry[pnJID] != "Alice" {
		t.Fatalf("1:1 chat should be seeded, got %q", w.chatRegistry[pnJID])
	}
}

// Item 2 / S1: a push name learned for the @lid alt must NOT override the
// saved address-book name reachable via LID→PN.
func TestLookupName_LIDPushNameDoesNotOverrideSavedName(t *testing.T) {
	w := newTestClient()
	pnJID, _ := types.ParseJID("5511999999999@s.whatsapp.net")
	w.ContactCache[pnJID.String()] = Contact{JID: pnJID, Name: "Mãe"}

	lidJID, _ := types.ParseJID("123456789012345@lid")
	w.lidPNCache = map[string]types.JID{lidJID.String(): pnJID}

	w.rememberPushName(lidJID, "Maria Silva") // her public WhatsApp name

	if got := w.LookupName(lidJID); got != "Mãe" {
		t.Fatalf("saved name must win over @lid push name, got %q", got)
	}
}

// Item 4: a push name learned in memory must survive FetchContacts rebuilding
// ContactCache from a DB row that has no saved name.
func TestLookupName_PushNameSurvivesContactCacheRebuild(t *testing.T) {
	w := newTestClient()
	jid, _ := types.ParseJID("5511999999999@s.whatsapp.net")
	w.rememberPushName(jid, "Bob")

	// Simulate FetchContacts replacing ContactCache with a nameless DB row.
	w.ContactCache = map[string]Contact{jid.String(): {JID: jid, Name: ""}}

	if got := w.LookupName(jid); got != "Bob" {
		t.Fatalf("push name must survive a ContactCache rebuild, got %q", got)
	}
}

// Item 8: a contact known only by push name who renames should show the new
// name (the old guard froze it).
func TestLookupName_ReflectsPushNameChange(t *testing.T) {
	w := newTestClient()
	jid, _ := types.ParseJID("5511999999999@s.whatsapp.net")
	w.rememberPushName(jid, "Bob")
	w.rememberPushName(jid, "Bobby") // rename

	if got := w.LookupName(jid); got != "Bobby" {
		t.Fatalf("push name change should be reflected, got %q", got)
	}
}

// Item 7: with the live push name cached, the sender bubble (resolveDisplayName
// with the live push) and the reply-quote path (LookupName, no push arg) must
// agree — one resolver, one precedence, no divergent labels.
func TestResolveDisplayName_SenderAndLookupAgree(t *testing.T) {
	w := newTestClient()
	jid, _ := types.ParseJID("5511999999999@s.whatsapp.net")
	w.chatRegistry[jid.String()] = "Stale Seed"
	w.rememberPushName(jid, "Bobby") // eventHandler caches the live push name

	bubble := w.resolveDisplayName(jid, "Bobby")
	reply := w.LookupName(jid)
	if bubble != reply {
		t.Fatalf("sender and reply names disagree: bubble=%q reply=%q", bubble, reply)
	}
	if bubble != "Bobby" {
		t.Fatalf("fresh push name should win over stale registry, got %q", bubble)
	}
}

func TestScheduleContactRefreshUsesContactsUpdatedCallback(t *testing.T) {
	w := newTestClient()
	connected := make(chan struct{}, 1)
	contactsUpdated := make(chan struct{}, 1)
	w.OnConnected = func() { connected <- struct{}{} }
	w.OnContactsUpdated = func() { contactsUpdated <- struct{}{} }

	w.scheduleContactRefresh()

	select {
	case <-contactsUpdated:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for contacts-updated callback")
	}
	select {
	case <-connected:
		t.Fatal("contact refresh must not fire OnConnected")
	default:
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

func TestGetChats_UsesResolvedNameForDirectLIDChat(t *testing.T) {
	w := newTestClient()
	pnJID, _ := types.ParseJID("5511999999999@s.whatsapp.net")
	lidJID, _ := types.ParseJID("123456789012345@lid")

	w.ContactCache[pnJID.String()] = Contact{JID: pnJID, Name: "Mãe"}
	w.lidPNCache[lidJID.String()] = pnJID
	w.chatRegistry[lidJID.String()] = "Maria Silva"

	chats, err := w.GetChats()
	if err != nil {
		t.Fatalf("GetChats: %v", err)
	}
	for _, chat := range chats {
		if chat.JID.String() != lidJID.String() {
			continue
		}
		if got := chat.DisplayName; got != "Mãe" {
			t.Fatalf("GetChats must use resolved saved name for LID chat, got %q", got)
		}
		return
	}
	t.Fatalf("missing LID chat %s in %#v", lidJID, chats)
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
