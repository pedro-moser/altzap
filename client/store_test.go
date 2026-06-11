package client

import (
	"database/sql"
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

// Item 11: persistOwn must surface a persistence failure rather than swallow
// it, so the send path can tell whether the local record was written.
func TestPersistOwn_ReturnsErrorWhenStoreClosed(t *testing.T) {
	s := openTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	w := &WhatsAppClient{msgStore: s}
	if err := w.persistOwn(SavedMessage{ChatJID: "c@s.whatsapp.net", ID: "X", Text: "hi"}); err == nil {
		t.Fatal("persistOwn must return the InsertBatch error when the store is closed")
	}
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

// legacySchemaSQL is the messages schema as it shipped before the
// forwarded/gif_playback columns existed — used to simulate a production
// database that predates the first ALTER TABLE migration.
const legacySchemaSQL = `
CREATE TABLE IF NOT EXISTS messages (
    chat_jid          TEXT    NOT NULL,
    id                TEXT    NOT NULL,
    sender_jid        TEXT    NOT NULL DEFAULT '',
    sender_name       TEXT    NOT NULL DEFAULT '',
    text              TEXT    NOT NULL DEFAULT '',
    ts                INTEGER NOT NULL,
    from_me           INTEGER NOT NULL DEFAULT 0,
    media_type        TEXT    NOT NULL DEFAULT '',
    media_path        TEXT    NOT NULL DEFAULT '',
    mimetype          TEXT    NOT NULL DEFAULT '',
    filename          TEXT    NOT NULL DEFAULT '',
    file_size         INTEGER NOT NULL DEFAULT 0,
    width             INTEGER NOT NULL DEFAULT 0,
    height            INTEGER NOT NULL DEFAULT 0,
    duration          INTEGER NOT NULL DEFAULT 0,
    thumb_b64         TEXT    NOT NULL DEFAULT '',
    reply_to_id            TEXT NOT NULL DEFAULT '',
    reply_to_sender_jid    TEXT NOT NULL DEFAULT '',
    reply_to_sender_name   TEXT NOT NULL DEFAULT '',
    reply_to_text          TEXT NOT NULL DEFAULT '',
    reply_to_media_type    TEXT NOT NULL DEFAULT '',
    reactions_json    TEXT    NOT NULL DEFAULT '[]',
    edited            INTEGER NOT NULL DEFAULT 0,
    edited_at         INTEGER NOT NULL DEFAULT 0,
    deleted           INTEGER NOT NULL DEFAULT 0,
    deleted_at        INTEGER NOT NULL DEFAULT 0,
    status            TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (chat_jid, id)
);
CREATE INDEX IF NOT EXISTS idx_messages_chat_ts ON messages(chat_jid, ts);
`

func TestMigrateSchema_AddsColumnsPreservingData(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "messages.db")

	// Seed a database with the pre-migration schema and one raw row.
	raw, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := raw.Exec(legacySchemaSQL); err != nil {
		t.Fatalf("apply legacy schema: %v", err)
	}
	if _, err := raw.Exec(
		`INSERT INTO messages (chat_jid, id, text, ts, from_me) VALUES (?, ?, ?, ?, 1)`,
		"chat@s.whatsapp.net", "OLD1", "antiga", 1700000000,
	); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	// Opening through the store must run the migration.
	s, err := OpenMessageStore(dbPath)
	if err != nil {
		t.Fatalf("OpenMessageStore (migrating): %v", err)
	}

	cols := map[string]bool{}
	rows, err := s.db.Query(`PRAGMA table_info(messages)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, ctype      string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		cols[name] = true
	}
	rows.Close()
	for _, m := range columnMigrations {
		if !cols[m.name] {
			t.Errorf("column %q missing after migration", m.name)
		}
	}

	// Legacy data must survive with the new fields zeroed.
	got, ok, err := s.LoadMessage("chat@s.whatsapp.net", "OLD1")
	if err != nil || !ok {
		t.Fatalf("LoadMessage after migration: ok=%v err=%v", ok, err)
	}
	if got.Text != "antiga" || !got.FromMe || got.Forwarded || got.GifPlayback {
		t.Errorf("legacy row mangled: %+v", got)
	}

	// And the new columns must round-trip through Insert and Patch.
	rec := SavedMessage{ChatJID: "chat@s.whatsapp.net", ID: "NEW1", Text: "nova",
		Timestamp: 1700000001, Forwarded: true, GifPlayback: true}
	if err := s.Insert(rec); err != nil {
		t.Fatalf("Insert post-migration: %v", err)
	}
	if err := s.Patch(rec.ChatJID, rec.ID, func(r *SavedMessage) bool {
		r.Status = "read"
		return true
	}); err != nil {
		t.Fatalf("Patch post-migration: %v", err)
	}
	got2, _, err := s.LoadMessage(rec.ChatJID, rec.ID)
	if err != nil {
		t.Fatalf("LoadMessage NEW1: %v", err)
	}
	if !got2.Forwarded || !got2.GifPlayback || got2.Status != "read" {
		t.Errorf("Patch must not wipe migrated columns: %+v", got2)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Idempotence: a second open re-runs migrateSchema as a no-op.
	s2, err := OpenMessageStore(dbPath)
	if err != nil {
		t.Fatalf("re-open after migration: %v", err)
	}
	defer s2.Close()
}

func TestInsertAndLoadChat_Roundtrip(t *testing.T) {
	s := openTestStore(t)
	rec := SavedMessage{
		ChatJID:           "chat@s.whatsapp.net",
		ID:                "ABC123",
		SenderJID:         "alice@s.whatsapp.net",
		SenderName:        "Alice",
		Text:              "hello world",
		Timestamp:         1700000000,
		FromMe:            false,
		MediaType:         "image",
		MediaPath:         "media/x.jpg",
		Mimetype:          "image/jpeg",
		FileName:          "x.jpg",
		FileSize:          12345,
		Width:             800,
		Height:            600,
		Duration:          0,
		ThumbB64:          "BASE64DATA==",
		ReplyToID:         "PREV1",
		ReplyToSenderJID:  "bob@s.whatsapp.net",
		ReplyToSenderName: "Bob",
		ReplyToText:       "what?",
		ReplyToMediaType:  "",
		Reactions: []SavedReaction{
			{Emoji: "❤️", SenderJID: "carol@s.whatsapp.net", SenderName: "Carol", Timestamp: 1700000005},
		},
		Edited:      false,
		EditedAt:    0,
		Deleted:     false,
		DeletedAt:   0,
		Status:      "",
		Forwarded:   true,
		GifPlayback: true,
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

func TestLoadMessage_FoundAndMissing(t *testing.T) {
	s := openTestStore(t)
	rec := SavedMessage{
		ChatJID:   "chat@s.whatsapp.net",
		ID:        "ABC123",
		Text:      "hello",
		Timestamp: 1700000000,
		MediaType: "voice",
		MediaPath: "media/chat/ABC123.ogg",
	}
	if err := s.Insert(rec); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, ok, err := s.LoadMessage(rec.ChatJID, rec.ID)
	if err != nil {
		t.Fatalf("LoadMessage: %v", err)
	}
	if !ok {
		t.Fatal("LoadMessage should report ok=true for existing record")
	}
	if !reflect.DeepEqual(got, rec) {
		t.Fatalf("roundtrip mismatch:\nwant: %+v\ngot:  %+v", rec, got)
	}

	if _, ok, err := s.LoadMessage(rec.ChatJID, "missing"); err != nil || ok {
		t.Fatalf("missing LoadMessage: ok=%v err=%v", ok, err)
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

func TestInsertBatch_SingleTransaction(t *testing.T) {
	s := openTestStore(t)
	const n = 1000
	chat := "chat@s.whatsapp.net"
	batch := make([]SavedMessage, n)
	for i := 0; i < n; i++ {
		batch[i] = SavedMessage{
			ChatJID:   chat,
			ID:        fmt.Sprintf("M%04d", i),
			Text:      fmt.Sprintf("msg %d", i),
			Timestamp: int64(i),
		}
	}
	if err := s.InsertBatch(batch); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	got, err := s.LoadChat(chat)
	if err != nil {
		t.Fatalf("LoadChat: %v", err)
	}
	if len(got) != n {
		t.Fatalf("expected %d records, got %d", n, len(got))
	}
	for i := 0; i < n; i++ {
		if got[i].ID != batch[i].ID {
			t.Errorf("position %d: want id %s, got %s", i, batch[i].ID, got[i].ID)
			break
		}
	}
}

func TestInsertBatch_EmptySliceIsNoOp(t *testing.T) {
	s := openTestStore(t)
	if err := s.InsertBatch(nil); err != nil {
		t.Fatalf("InsertBatch(nil): %v", err)
	}
	if err := s.InsertBatch([]SavedMessage{}); err != nil {
		t.Fatalf("InsertBatch([]): %v", err)
	}
}

func TestPatch_MissingRecordIsNoOp(t *testing.T) {
	s := openTestStore(t)
	called := false
	err := s.Patch("nope@s.whatsapp.net", "MISSING", func(rec *SavedMessage) bool {
		called = true
		return true
	})
	if err != nil {
		t.Fatalf("Patch on missing: %v", err)
	}
	if called {
		t.Errorf("mutate fn should not be called when record is missing")
	}
}

func TestPatch_MutateReturnsFalseLeavesRecord(t *testing.T) {
	s := openTestStore(t)
	rec := SavedMessage{
		ChatJID: "c@x", ID: "M1", Text: "original", Timestamp: 100, Status: "",
	}
	if err := s.Insert(rec); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	err := s.Patch("c@x", "M1", func(r *SavedMessage) bool {
		r.Status = "delivered" // would mutate, but we say no
		return false
	})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	got, _ := s.LoadChat("c@x")
	if got[0].Status != "" {
		t.Errorf("Status should be unchanged, got %q", got[0].Status)
	}
	if got[0].Text != "original" {
		t.Errorf("Text should be unchanged, got %q", got[0].Text)
	}
}

func TestPatch_MutateReturnsTrueWritesUpdate(t *testing.T) {
	s := openTestStore(t)
	rec := SavedMessage{
		ChatJID: "c@x", ID: "M1", Text: "original", Timestamp: 100,
	}
	if err := s.Insert(rec); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	err := s.Patch("c@x", "M1", func(r *SavedMessage) bool {
		r.Status = "read"
		r.Reactions = []SavedReaction{
			{Emoji: "👍", SenderJID: "alice@x", SenderName: "Alice", Timestamp: 200},
		}
		return true
	})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	got, _ := s.LoadChat("c@x")
	if got[0].Status != "read" {
		t.Errorf("Status: want 'read', got %q", got[0].Status)
	}
	if len(got[0].Reactions) != 1 || got[0].Reactions[0].Emoji != "👍" {
		t.Errorf("Reactions not persisted: %+v", got[0].Reactions)
	}
}

func TestChatSummaries_OrdersByLastTimestampAndReturnsLastMessage(t *testing.T) {
	s := openTestStore(t)
	// Chat A: 2 messages, last at ts=200
	_ = s.Insert(SavedMessage{ChatJID: "a@x", ID: "A1", Text: "first", Timestamp: 100})
	_ = s.Insert(SavedMessage{ChatJID: "a@x", ID: "A2", Text: "latest A", Timestamp: 200, FromMe: true, SenderName: "Me"})
	// Chat B: 1 message at ts=300 (most recent across all)
	_ = s.Insert(SavedMessage{ChatJID: "b@x", ID: "B1", Text: "latest B", Timestamp: 300, SenderName: "Bob", MediaType: ""})
	// Chat C: 1 message at ts=50 (oldest)
	_ = s.Insert(SavedMessage{ChatJID: "c@x", ID: "C1", Text: "", Timestamp: 50, SenderName: "Carol", MediaType: "image"})

	sums, err := s.ChatSummaries()
	if err != nil {
		t.Fatalf("ChatSummaries: %v", err)
	}
	if len(sums) != 3 {
		t.Fatalf("expected 3 summaries, got %d", len(sums))
	}
	// Order: B (300), A (200), C (50)
	wantOrder := []string{"b@x", "a@x", "c@x"}
	for i, jid := range wantOrder {
		if sums[i].ChatJID != jid {
			t.Errorf("position %d: want %s, got %s", i, jid, sums[i].ChatJID)
		}
	}
	// Payload checks
	if sums[0].LastText != "latest B" || sums[0].LastSenderName != "Bob" {
		t.Errorf("B summary mismatch: %+v", sums[0])
	}
	if sums[1].LastText != "latest A" || !sums[1].LastFromMe {
		t.Errorf("A summary mismatch: %+v", sums[1])
	}
	if sums[2].LastMediaType != "image" {
		t.Errorf("C summary mismatch: %+v", sums[2])
	}
}

func TestChatSummaries_EmptyDB(t *testing.T) {
	s := openTestStore(t)
	sums, err := s.ChatSummaries()
	if err != nil {
		t.Fatalf("ChatSummaries: %v", err)
	}
	if len(sums) != 0 {
		t.Errorf("expected 0 summaries, got %d", len(sums))
	}
}
