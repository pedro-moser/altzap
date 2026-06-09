package ui

import (
	"testing"

	"altzap/client"
	"go.mau.fi/whatsmeow/types"
)

func sortChat(user string, pinned bool, ts int64) client.Chat {
	return client.Chat{
		JID:             types.NewJID(user, types.DefaultUserServer),
		Pinned:          pinned,
		LastMessageTime: ts,
	}
}

func TestPinnedFirstKeepsRecencyWithinPartitions(t *testing.T) {
	in := []client.Chat{
		sortChat("a", false, 500),
		sortChat("b", true, 400),
		sortChat("c", false, 300),
		sortChat("d", true, 200),
	}
	got := pinnedFirst(in)
	wantOrder := []string{"b", "d", "a", "c"}
	if len(got) != len(wantOrder) {
		t.Fatalf("len = %d, want %d", len(got), len(wantOrder))
	}
	for i, user := range wantOrder {
		if got[i].JID.User != user {
			t.Fatalf("pos %d = %s, want %s (got order %v)", i, got[i].JID.User, user, got)
		}
	}
}

func TestPinnedFirstNoPinsIsIdentity(t *testing.T) {
	in := []client.Chat{sortChat("a", false, 2), sortChat("b", false, 1)}
	got := pinnedFirst(in)
	if len(got) != 2 || got[0].JID.User != "a" || got[1].JID.User != "b" {
		t.Fatalf("unexpected order: %v", got)
	}
}

func TestPinnedFirstEmpty(t *testing.T) {
	if got := pinnedFirst(nil); len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}
