package client

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func openTestStore(t *testing.T) *MessageStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "messages.db")
	s, err := OpenMessageStore(dbPath)
	if err != nil {
		t.Fatalf("OpenMessageStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenMessageStore_CreatesSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "messages.db")
	s, err := OpenMessageStore(dbPath)
	if err != nil {
		t.Fatalf("OpenMessageStore: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Re-opening must succeed (CREATE TABLE IF NOT EXISTS).
	s2, err := OpenMessageStore(dbPath)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	defer s2.Close()

	var name string
	err = s2.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='messages'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("schema check: %v", err)
	}
	if name != "messages" {
		t.Errorf("expected table 'messages', got %q", name)
	}
}

func TestInsertAndLoadChat_Roundtrip(t *testing.T) {
	s := openTestStore(t)
	rec := SavedMessage{
		ChatJID:   "chat@s.whatsapp.net",
		ID:        "ABC123",
		SenderJID: "alice@s.whatsapp.net",
		SenderName: "Alice",
		Text:      "hello world",
		Timestamp: 1700000000,
		FromMe:    false,
		MediaType: "image",
		MediaPath: "media/x.jpg",
		Mimetype:  "image/jpeg",
		FileName:  "x.jpg",
		FileSize:  12345,
		Width:     800,
		Height:    600,
		Duration:  0,
		ThumbB64:  "BASE64DATA==",
		ReplyToID:         "PREV1",
		ReplyToSenderJID:  "bob@s.whatsapp.net",
		ReplyToSenderName: "Bob",
		ReplyToText:       "what?",
		ReplyToMediaType:  "",
		Reactions: []SavedReaction{
			{Emoji: "❤️", SenderJID: "carol@s.whatsapp.net", SenderName: "Carol", Timestamp: 1700000005},
		},
		Edited:    false,
		EditedAt:  0,
		Deleted:   false,
		DeletedAt: 0,
		Status:    "",
	}

	if err := s.Insert(rec); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := s.LoadChat("chat@s.whatsapp.net")
	if err != nil {
		t.Fatalf("LoadChat: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 record, got %d", len(got))
	}
	if !reflect.DeepEqual(got[0], rec) {
		t.Errorf("roundtrip mismatch:\nwant: %+v\ngot:  %+v", rec, got[0])
	}
}

func TestLoadChat_OrdersByTimestampAscending(t *testing.T) {
	s := openTestStore(t)
	chat := "chat@s.whatsapp.net"
	// Insert out of order: ts=300, 100, 200
	for _, ts := range []int64{300, 100, 200} {
		if err := s.Insert(SavedMessage{
			ChatJID:   chat,
			ID:        fmt.Sprintf("M%d", ts),
			Text:      "x",
			Timestamp: ts,
		}); err != nil {
			t.Fatalf("Insert ts=%d: %v", ts, err)
		}
	}
	got, err := s.LoadChat(chat)
	if err != nil {
		t.Fatalf("LoadChat: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 records, got %d", len(got))
	}
	for i, want := range []int64{100, 200, 300} {
		if got[i].Timestamp != want {
			t.Errorf("position %d: want ts=%d, got ts=%d", i, want, got[i].Timestamp)
		}
	}
}
