package ui

import (
	"testing"

	"altzap/client"

	"go.mau.fi/whatsmeow/types"
)

func chatJID(s string) types.JID {
	jid, _ := types.ParseJID(s)
	return jid
}

func TestNextChatJID_EmptyList(t *testing.T) {
	if got := nextChatJID(nil, "anything"); got != "" {
		t.Fatalf("empty list should return \"\", got %q", got)
	}
}

func TestNextChatJID_NoCurrentOpensFirst(t *testing.T) {
	chats := []client.Chat{
		{JID: chatJID("5511A@s.whatsapp.net")},
		{JID: chatJID("5511B@s.whatsapp.net")},
	}
	if got := nextChatJID(chats, ""); got != "5511A@s.whatsapp.net" {
		t.Fatalf("no current should open first, got %q", got)
	}
}

func TestNextChatJID_CurrentNotFoundOpensFirst(t *testing.T) {
	chats := []client.Chat{
		{JID: chatJID("5511A@s.whatsapp.net")},
		{JID: chatJID("5511B@s.whatsapp.net")},
	}
	if got := nextChatJID(chats, "stale@s.whatsapp.net"); got != "5511A@s.whatsapp.net" {
		t.Fatalf("unknown current should fall back to first, got %q", got)
	}
}

func TestNextChatJID_StepsForward(t *testing.T) {
	chats := []client.Chat{
		{JID: chatJID("5511A@s.whatsapp.net")},
		{JID: chatJID("5511B@s.whatsapp.net")},
		{JID: chatJID("5511C@s.whatsapp.net")},
	}
	if got := nextChatJID(chats, "5511A@s.whatsapp.net"); got != "5511B@s.whatsapp.net" {
		t.Fatalf("want B, got %q", got)
	}
	if got := nextChatJID(chats, "5511B@s.whatsapp.net"); got != "5511C@s.whatsapp.net" {
		t.Fatalf("want C, got %q", got)
	}
}

func TestNextChatJID_LastClamps(t *testing.T) {
	chats := []client.Chat{
		{JID: chatJID("5511A@s.whatsapp.net")},
		{JID: chatJID("5511B@s.whatsapp.net")},
	}
	if got := nextChatJID(chats, "5511B@s.whatsapp.net"); got != "" {
		t.Fatalf("last + next should clamp to \"\", got %q", got)
	}
}

func TestPrevChatJID_FirstClamps(t *testing.T) {
	chats := []client.Chat{
		{JID: chatJID("5511A@s.whatsapp.net")},
		{JID: chatJID("5511B@s.whatsapp.net")},
	}
	if got := prevChatJID(chats, "5511A@s.whatsapp.net"); got != "" {
		t.Fatalf("first + prev should clamp to \"\", got %q", got)
	}
}

func TestPrevChatJID_StepsBackward(t *testing.T) {
	chats := []client.Chat{
		{JID: chatJID("5511A@s.whatsapp.net")},
		{JID: chatJID("5511B@s.whatsapp.net")},
		{JID: chatJID("5511C@s.whatsapp.net")},
	}
	if got := prevChatJID(chats, "5511C@s.whatsapp.net"); got != "5511B@s.whatsapp.net" {
		t.Fatalf("want B, got %q", got)
	}
}

func TestPrevChatJID_NoCurrentOpensFirst(t *testing.T) {
	chats := []client.Chat{
		{JID: chatJID("5511A@s.whatsapp.net")},
	}
	if got := prevChatJID(chats, ""); got != "5511A@s.whatsapp.net" {
		t.Fatalf("no current should open first, got %q", got)
	}
}

func TestLatestNonDeletedMessageID_Empty(t *testing.T) {
	if got := latestNonDeletedMessageID(nil); got != "" {
		t.Fatalf("empty list should return \"\", got %q", got)
	}
}

func TestLatestNonDeletedMessageID_PicksMostRecent(t *testing.T) {
	msgs := []*Message{
		{ID: "m1"},
		{ID: "m2"},
		{ID: "m3"},
	}
	if got := latestNonDeletedMessageID(msgs); got != "m3" {
		t.Fatalf("want m3, got %q", got)
	}
}

func TestLatestNonDeletedMessageID_SkipsDeletedTail(t *testing.T) {
	msgs := []*Message{
		{ID: "m1"},
		{ID: "m2"},
		{ID: "m3", Deleted: true},
	}
	if got := latestNonDeletedMessageID(msgs); got != "m2" {
		t.Fatalf("should skip deleted tail, want m2, got %q", got)
	}
}

func TestLatestNonDeletedMessageID_AllDeleted(t *testing.T) {
	msgs := []*Message{
		{ID: "m1", Deleted: true},
		{ID: "m2", Deleted: true},
	}
	if got := latestNonDeletedMessageID(msgs); got != "" {
		t.Fatalf("all-deleted should return \"\", got %q", got)
	}
}

func TestNextNonDeletedMessageID_Steps(t *testing.T) {
	msgs := []*Message{
		{ID: "m1"},
		{ID: "m2"},
		{ID: "m3"},
	}
	if got := nextNonDeletedMessageID(msgs, "m1"); got != "m2" {
		t.Fatalf("want m2, got %q", got)
	}
	if got := nextNonDeletedMessageID(msgs, "m2"); got != "m3" {
		t.Fatalf("want m3, got %q", got)
	}
}

func TestNextNonDeletedMessageID_SkipsDeletedInMiddle(t *testing.T) {
	msgs := []*Message{
		{ID: "m1"},
		{ID: "m2", Deleted: true},
		{ID: "m3"},
	}
	if got := nextNonDeletedMessageID(msgs, "m1"); got != "m3" {
		t.Fatalf("should jump over deleted, want m3, got %q", got)
	}
}

func TestNextNonDeletedMessageID_LastClamps(t *testing.T) {
	msgs := []*Message{
		{ID: "m1"},
		{ID: "m2"},
	}
	if got := nextNonDeletedMessageID(msgs, "m2"); got != "m2" {
		t.Fatalf("last clamps to itself, got %q", got)
	}
}

func TestNextNonDeletedMessageID_UnknownCurrent(t *testing.T) {
	msgs := []*Message{
		{ID: "m1"},
	}
	if got := nextNonDeletedMessageID(msgs, "ghost"); got != "ghost" {
		t.Fatalf("unknown id should pass through unchanged, got %q", got)
	}
}

func TestPrevNonDeletedMessageID_Steps(t *testing.T) {
	msgs := []*Message{
		{ID: "m1"},
		{ID: "m2"},
		{ID: "m3"},
	}
	if got := prevNonDeletedMessageID(msgs, "m3"); got != "m2" {
		t.Fatalf("want m2, got %q", got)
	}
}

func TestPrevNonDeletedMessageID_SkipsDeletedInMiddle(t *testing.T) {
	msgs := []*Message{
		{ID: "m1"},
		{ID: "m2", Deleted: true},
		{ID: "m3"},
	}
	if got := prevNonDeletedMessageID(msgs, "m3"); got != "m1" {
		t.Fatalf("should jump over deleted, want m1, got %q", got)
	}
}

func TestPrevNonDeletedMessageID_FirstClamps(t *testing.T) {
	msgs := []*Message{
		{ID: "m1"},
		{ID: "m2"},
	}
	if got := prevNonDeletedMessageID(msgs, "m1"); got != "m1" {
		t.Fatalf("first clamps to itself, got %q", got)
	}
}
