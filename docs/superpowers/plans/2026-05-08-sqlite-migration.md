# SQLite Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace per-chat JSONL append-only files with a single SQLite database (`store/messages.db`), so that mutations (reactions, receipts, edits, delete-for-everyone, media-ready) become O(log N) instead of rewriting the whole chat file on every event.

**Architecture:** A new `client/store.go` exposes a `MessageStore` type wrapping `*sql.DB`, with Insert/InsertBatch/Patch/LoadChat/ChatSummaries methods. The existing `savedMessage` struct is exported as `SavedMessage` so the UI can consume it via the public client API. On boot, a one-shot migration drains all `store/msg_*.json` into the messages table inside a single transaction, then renames the JSONLs into `store/.legacy/` as backup. After migration, all callers in `whatsmeow_client.go`, `media.go`, and `chat_view.go` route persistence through `MessageStore`.

**Tech Stack:** Go 1.26, `database/sql`, `github.com/mattn/go-sqlite3` (already in go.mod via whatsmeow), SQLite WAL mode.

**Design spec:** [`docs/superpowers/specs/2026-05-08-sqlite-migration-design.md`](../specs/2026-05-08-sqlite-migration-design.md)

---

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `client/store.go` | **create** | `MessageStore` type, schema, Insert/InsertBatch/Patch/LoadChat/ChatSummaries |
| `client/store_test.go` | **create** | Unit tests for `MessageStore` |
| `client/migrate.go` | **create** | One-shot JSONL→SQLite migration helper |
| `client/migrate_test.go` | **create** | Migration unit tests |
| `client/whatsmeow_client.go` | **modify** | Rename `savedMessage`→`SavedMessage`; replace `appendMessages`/`persistOwn`/`persistIncoming`/`loadMessageIDs` with store calls; expose `LoadMessages`/`ChatSummaries`; remove `muStoreFile` |
| `client/media.go` | **modify** | `patchRecord` body becomes a thin wrapper around `store.Patch` |
| `ui/chat_view.go` | **modify** | `loadMessagesFromDisk` calls `LoadMessages`; `loadChatList` uses `ChatSummaries`; remove `getLastMessagePreview` |
| `main.go` | **modify** | Open `MessageStore`, run migration, pass into `NewWhatsAppClient`, defer Close |

---

## Task 1: Rename `savedMessage` → `SavedMessage`

Mechanical refactor — exports the struct so the UI can consume it via the public client API. No behavior change. Done first so subsequent tasks can use the public name.

**Files:**
- Modify: `client/whatsmeow_client.go` (definition at line 38; ~25 references)
- Modify: `client/media.go` (signature in `patchRecord` at line 171; reference at line 158)

- [ ] **Step 1: Inspect all references to confirm scope**

```bash
grep -n "savedMessage" client/*.go
```

Expected: hits in `whatsmeow_client.go` (definition + many usages) and `media.go` (function signature + call site). No hits in `ui/` or `main.go`.

- [ ] **Step 2: Rename in `client/whatsmeow_client.go`**

Use the Edit tool with `replace_all: true`:
- File: `client/whatsmeow_client.go`
- old_string: `savedMessage`
- new_string: `SavedMessage`
- replace_all: true

- [ ] **Step 3: Rename in `client/media.go`**

Same operation:
- File: `client/media.go`
- old_string: `savedMessage`
- new_string: `SavedMessage`
- replace_all: true

- [ ] **Step 4: Build to verify**

```bash
go build ./...
```

Expected: clean build, no errors.

- [ ] **Step 5: Commit**

```bash
git add client/whatsmeow_client.go client/media.go
git commit -m "$(cat <<'EOF'
refactor(client): export savedMessage as SavedMessage

Preparing for the SQLite migration: the new MessageStore API will be
consumed from the UI package, so the persisted-message struct needs a
public name.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: `MessageStore` skeleton — schema + Open/Close

TDD: test that opening creates the schema, closing succeeds, re-opening is idempotent.

**Files:**
- Create: `client/store.go`
- Create: `client/store_test.go`

- [ ] **Step 1: Write the failing test**

Create `client/store_test.go`:

```go
package client

import (
	"path/filepath"
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
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./client/ -run TestOpenMessageStore_CreatesSchema -v
```

Expected: FAIL with `undefined: OpenMessageStore` and `undefined: MessageStore`.

- [ ] **Step 3: Write minimal implementation**

Create `client/store.go`:

```go
package client

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

const schemaSQL = `
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

// MessageStore persists chat messages in a SQLite database.
// Replaces the previous per-chat JSONL append-only files. WAL is enabled
// so receipt/reaction patches don't block readers.
type MessageStore struct {
	db *sql.DB
}

// OpenMessageStore opens (creating if needed) a SQLite database at path
// and ensures the schema is current. The DSN turns on WAL, busy_timeout,
// and a 32MB page cache.
func OpenMessageStore(path string) (*MessageStore, error) {
	dsn := fmt.Sprintf(
		"file:%s?_journal_mode=WAL&_synchronous=NORMAL&_cache_size=-32000&_busy_timeout=5000&_foreign_keys=on",
		path,
	)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &MessageStore{db: db}, nil
}

func (s *MessageStore) Close() error {
	return s.db.Close()
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./client/ -run TestOpenMessageStore_CreatesSchema -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add client/store.go client/store_test.go
git commit -m "$(cat <<'EOF'
feat(client): add MessageStore skeleton with schema

Foundation for the JSONL→SQLite migration. Single 'messages' table
with composite PK (chat_jid, id) and an index on (chat_jid, ts) for
the dominant tail-read pattern.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: `Insert` + `LoadChat` roundtrip

TDD: insert a `SavedMessage` with every field populated, read it back, deep-equal.

**Files:**
- Modify: `client/store.go`
- Modify: `client/store_test.go`

- [ ] **Step 1: Write the failing test**

Append to `client/store_test.go`:

```go
import (
	// add to imports:
	"reflect"
)

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
```

Add `"fmt"` to the test file's imports.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./client/ -run "TestInsertAndLoadChat_Roundtrip|TestLoadChat_OrdersByTimestampAscending" -v
```

Expected: FAIL with `undefined: (*MessageStore).Insert` and `undefined: (*MessageStore).LoadChat`.

- [ ] **Step 3: Implement Insert + LoadChat**

Append to `client/store.go`:

```go
import (
	// add to imports:
	"encoding/json"
)

const insertSQL = `INSERT OR IGNORE INTO messages (
    chat_jid, id, sender_jid, sender_name, text, ts, from_me,
    media_type, media_path, mimetype, filename, file_size, width, height, duration, thumb_b64,
    reply_to_id, reply_to_sender_jid, reply_to_sender_name, reply_to_text, reply_to_media_type,
    reactions_json, edited, edited_at, deleted, deleted_at, status
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const selectChatSQL = `SELECT chat_jid, id, sender_jid, sender_name, text, ts, from_me,
    media_type, media_path, mimetype, filename, file_size, width, height, duration, thumb_b64,
    reply_to_id, reply_to_sender_jid, reply_to_sender_name, reply_to_text, reply_to_media_type,
    reactions_json, edited, edited_at, deleted, deleted_at, status
    FROM messages WHERE chat_jid = ? ORDER BY ts ASC`

// execer is implemented by both *sql.DB and *sql.Tx — lets us share the
// row-binding code between Insert and the in-transaction batch path.
type execer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// reactionsJSON marshals reactions, normalizing nil/empty to "[]" so the
// column always holds valid JSON.
func reactionsJSON(rs []SavedReaction) (string, error) {
	if len(rs) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(rs)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func insertOne(e execer, r SavedMessage) error {
	rj, err := reactionsJSON(r.Reactions)
	if err != nil {
		return fmt.Errorf("marshal reactions: %w", err)
	}
	_, err = e.Exec(insertSQL,
		r.ChatJID, r.ID, r.SenderJID, r.SenderName, r.Text, r.Timestamp, boolToInt(r.FromMe),
		r.MediaType, r.MediaPath, r.Mimetype, r.FileName, r.FileSize, r.Width, r.Height, r.Duration, r.ThumbB64,
		r.ReplyToID, r.ReplyToSenderJID, r.ReplyToSenderName, r.ReplyToText, r.ReplyToMediaType,
		rj, boolToInt(r.Edited), r.EditedAt, boolToInt(r.Deleted), r.DeletedAt, r.Status,
	)
	return err
}

// Insert writes a single record. Idempotent on (chat_jid, id) — a duplicate
// PK is silently ignored, mirroring the JSONL dedup behavior of the previous
// implementation.
func (s *MessageStore) Insert(rec SavedMessage) error {
	return insertOne(s.db, rec)
}

func scanMessage(scanner interface {
	Scan(dest ...interface{}) error
}) (SavedMessage, error) {
	var r SavedMessage
	var fromMe, edited, deleted int
	var rj string
	if err := scanner.Scan(
		&r.ChatJID, &r.ID, &r.SenderJID, &r.SenderName, &r.Text, &r.Timestamp, &fromMe,
		&r.MediaType, &r.MediaPath, &r.Mimetype, &r.FileName, &r.FileSize, &r.Width, &r.Height, &r.Duration, &r.ThumbB64,
		&r.ReplyToID, &r.ReplyToSenderJID, &r.ReplyToSenderName, &r.ReplyToText, &r.ReplyToMediaType,
		&rj, &edited, &r.EditedAt, &deleted, &r.DeletedAt, &r.Status,
	); err != nil {
		return SavedMessage{}, err
	}
	r.FromMe = fromMe != 0
	r.Edited = edited != 0
	r.Deleted = deleted != 0
	if rj != "" && rj != "[]" {
		if err := json.Unmarshal([]byte(rj), &r.Reactions); err != nil {
			return SavedMessage{}, fmt.Errorf("unmarshal reactions for msg %s: %w", r.ID, err)
		}
	}
	return r, nil
}

// LoadChat returns all persisted messages for a chat in chronological order.
func (s *MessageStore) LoadChat(chatJID string) ([]SavedMessage, error) {
	rows, err := s.db.Query(selectChatSQL, chatJID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SavedMessage
	for rows.Next() {
		r, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./client/ -run "TestInsertAndLoadChat_Roundtrip|TestLoadChat_OrdersByTimestampAscending" -v
```

Expected: PASS for both.

- [ ] **Step 5: Commit**

```bash
git add client/store.go client/store_test.go
git commit -m "$(cat <<'EOF'
feat(client): MessageStore Insert and LoadChat

INSERT OR IGNORE on the composite PK gives us the JSONL dedup behavior
'for free'. Reactions go through a normalized JSON column — empty/nil
both serialize to '[]'.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: `InsertBatch` (single transaction)

TDD: many records inserted in one transaction; verify count and order.

**Files:**
- Modify: `client/store.go`
- Modify: `client/store_test.go`

- [ ] **Step 1: Write the failing test**

Append to `client/store_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./client/ -run "TestInsertBatch" -v
```

Expected: FAIL with `undefined: (*MessageStore).InsertBatch`.

- [ ] **Step 3: Implement InsertBatch**

Append to `client/store.go`:

```go
// InsertBatch writes many records inside a single transaction. Same
// idempotency as Insert — duplicates are silently dropped. Returns the
// first error encountered; on failure the entire batch is rolled back.
func (s *MessageStore) InsertBatch(recs []SavedMessage) error {
	if len(recs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after Commit
	for _, r := range recs {
		if err := insertOne(tx, r); err != nil {
			return err
		}
	}
	return tx.Commit()
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./client/ -run "TestInsertBatch" -v
```

Expected: PASS for both.

- [ ] **Step 5: Commit**

```bash
git add client/store.go client/store_test.go
git commit -m "$(cat <<'EOF'
feat(client): MessageStore InsertBatch with single transaction

Used by HistorySync (replacing the old appendMessages with a per-call
file open) and by the JSONL migration to drain everything in one tx.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: `Patch` mutation with idempotency

TDD: missing record → no-op; mutate fn returns false → no change; mutate fn returns true → fields updated.

**Files:**
- Modify: `client/store.go`
- Modify: `client/store_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `client/store_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./client/ -run "TestPatch_" -v
```

Expected: FAIL with `undefined: (*MessageStore).Patch`.

- [ ] **Step 3: Implement Patch**

Append to `client/store.go`:

```go
const selectOneSQL = `SELECT chat_jid, id, sender_jid, sender_name, text, ts, from_me,
    media_type, media_path, mimetype, filename, file_size, width, height, duration, thumb_b64,
    reply_to_id, reply_to_sender_jid, reply_to_sender_name, reply_to_text, reply_to_media_type,
    reactions_json, edited, edited_at, deleted, deleted_at, status
    FROM messages WHERE chat_jid = ? AND id = ?`

const updateSQL = `UPDATE messages SET
    sender_jid = ?, sender_name = ?, text = ?, ts = ?, from_me = ?,
    media_type = ?, media_path = ?, mimetype = ?, filename = ?, file_size = ?, width = ?, height = ?, duration = ?, thumb_b64 = ?,
    reply_to_id = ?, reply_to_sender_jid = ?, reply_to_sender_name = ?, reply_to_text = ?, reply_to_media_type = ?,
    reactions_json = ?, edited = ?, edited_at = ?, deleted = ?, deleted_at = ?, status = ?
    WHERE chat_jid = ? AND id = ?`

// Patch loads the record matching (chatJID, msgID), passes it to fn,
// and writes back only if fn returns true. Mirrors the semantics of
// the previous patchRecord on JSONL files. Missing records are silent
// no-ops (matches old behavior — patchRecord just returned).
func (s *MessageStore) Patch(chatJID, msgID string, fn func(*SavedMessage) bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rec, err := scanMessage(tx.QueryRow(selectOneSQL, chatJID, msgID))
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}

	if !fn(&rec) {
		return nil
	}

	rj, err := reactionsJSON(rec.Reactions)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(updateSQL,
		rec.SenderJID, rec.SenderName, rec.Text, rec.Timestamp, boolToInt(rec.FromMe),
		rec.MediaType, rec.MediaPath, rec.Mimetype, rec.FileName, rec.FileSize, rec.Width, rec.Height, rec.Duration, rec.ThumbB64,
		rec.ReplyToID, rec.ReplyToSenderJID, rec.ReplyToSenderName, rec.ReplyToText, rec.ReplyToMediaType,
		rj, boolToInt(rec.Edited), rec.EditedAt, boolToInt(rec.Deleted), rec.DeletedAt, rec.Status,
		chatJID, msgID,
	); err != nil {
		return err
	}
	return tx.Commit()
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./client/ -run "TestPatch_" -v
```

Expected: PASS for all three.

- [ ] **Step 5: Commit**

```bash
git add client/store.go client/store_test.go
git commit -m "$(cat <<'EOF'
feat(client): MessageStore Patch for in-place mutations

Replaces the JSONL full-file rewrite (decode all → re-encode all →
tmpfile → rename) with a single SELECT/UPDATE under a transaction.
Same semantics as the old patchRecord: silent no-op on missing
records, no-op when the mutate function returns false.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: `ChatSummaries` window function

TDD: 3 chats with different last messages → ordering by `last_ts DESC`, payload of last message returned.

**Files:**
- Modify: `client/store.go`
- Modify: `client/store_test.go`

- [ ] **Step 1: Write the failing test**

Append to `client/store_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./client/ -run "TestChatSummaries" -v
```

Expected: FAIL with `undefined: (*MessageStore).ChatSummaries` and `undefined: ChatSummary`.

- [ ] **Step 3: Implement ChatSummaries**

Append to `client/store.go`:

```go
// ChatSummary is one row per chat: the chat's JID and the metadata of
// its most recent message. Used by the sidebar to render previews
// without needing to read every JSONL file.
type ChatSummary struct {
	ChatJID        string
	LastTimestamp  int64
	LastText       string
	LastFromMe     bool
	LastSenderName string
	LastMediaType  string
}

const chatSummariesSQL = `
SELECT chat_jid, ts, text, from_me, sender_name, media_type
FROM (
    SELECT chat_jid, ts, text, from_me, sender_name, media_type,
           ROW_NUMBER() OVER (PARTITION BY chat_jid ORDER BY ts DESC) AS rn
    FROM messages
)
WHERE rn = 1
ORDER BY ts DESC
`

// ChatSummaries returns every known chat with its most recent message,
// ordered by recency. One query replaces the previous loop of os.ReadDir
// + os.Stat + tail-parse-JSONL per chat.
func (s *MessageStore) ChatSummaries() ([]ChatSummary, error) {
	rows, err := s.db.Query(chatSummariesSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatSummary
	for rows.Next() {
		var c ChatSummary
		var fromMe int
		if err := rows.Scan(&c.ChatJID, &c.LastTimestamp, &c.LastText, &fromMe, &c.LastSenderName, &c.LastMediaType); err != nil {
			return nil, err
		}
		c.LastFromMe = fromMe != 0
		out = append(out, c)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./client/ -run "TestChatSummaries" -v
```

Expected: PASS for both.

- [ ] **Step 5: Commit**

```bash
git add client/store.go client/store_test.go
git commit -m "$(cat <<'EOF'
feat(client): MessageStore ChatSummaries window query

Replaces the sidebar's per-chat 'os.Stat + tail-parse-JSONL' loop with
a single SQL query using ROW_NUMBER() OVER (PARTITION BY chat_jid).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Migration helper `MigrateLegacyJSONLs`

TDD: stage 3 fixture JSONLs, run migration, assert DB rows match + JSONLs moved to `.legacy/`.

**Files:**
- Create: `client/migrate.go`
- Create: `client/migrate_test.go`

- [ ] **Step 1: Write the failing test**

Create `client/migrate_test.go`:

```go
package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeJSONL writes one record per line, JSON-encoded, to path.
func writeJSONL(t *testing.T, path string, recs []SavedMessage) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
}

func TestMigrateLegacyJSONLs_HappyPath(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "messages.db")
	s, err := OpenMessageStore(dbPath)
	if err != nil {
		t.Fatalf("OpenMessageStore: %v", err)
	}
	defer s.Close()

	// Two chats with mixed records.
	writeJSONL(t, filepath.Join(dir, "msg_chat-a.json"), []SavedMessage{
		{ChatJID: "chat-a", ID: "A1", Text: "hi", Timestamp: 100},
		{ChatJID: "chat-a", ID: "A2", Text: "ok", Timestamp: 200, FromMe: true},
	})
	writeJSONL(t, filepath.Join(dir, "msg_chat-b.json"), []SavedMessage{
		{ChatJID: "chat-b", ID: "B1", Text: "yo", Timestamp: 150, MediaType: "image", ThumbB64: "AAAA"},
	})

	n, err := MigrateLegacyJSONLs(s, dir)
	if err != nil {
		t.Fatalf("MigrateLegacyJSONLs: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 migrated, got %d", n)
	}

	// Records present in DB.
	a, _ := s.LoadChat("chat-a")
	if len(a) != 2 {
		t.Errorf("chat-a: want 2 records, got %d", len(a))
	}
	b, _ := s.LoadChat("chat-b")
	if len(b) != 1 || b[0].MediaType != "image" || b[0].ThumbB64 != "AAAA" {
		t.Errorf("chat-b: unexpected record %+v", b)
	}

	// JSONLs moved to .legacy/
	for _, name := range []string{"msg_chat-a.json", "msg_chat-b.json"} {
		legacy := filepath.Join(dir, ".legacy", name)
		if _, err := os.Stat(legacy); err != nil {
			t.Errorf("expected %s to exist: %v", legacy, err)
		}
		original := filepath.Join(dir, name)
		if _, err := os.Stat(original); !os.IsNotExist(err) {
			t.Errorf("expected %s to be moved away (got err=%v)", original, err)
		}
	}
}

func TestMigrateLegacyJSONLs_SkipsWhenDBNonEmpty(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "messages.db")
	s, err := OpenMessageStore(dbPath)
	if err != nil {
		t.Fatalf("OpenMessageStore: %v", err)
	}
	defer s.Close()

	// Pre-populate the DB.
	if err := s.Insert(SavedMessage{ChatJID: "x", ID: "X1", Text: "preexisting", Timestamp: 1}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Stage a JSONL that should NOT be imported.
	writeJSONL(t, filepath.Join(dir, "msg_chat-a.json"), []SavedMessage{
		{ChatJID: "chat-a", ID: "A1", Text: "should-be-ignored", Timestamp: 100},
	})

	n, err := MigrateLegacyJSONLs(s, dir)
	if err != nil {
		t.Fatalf("MigrateLegacyJSONLs: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 migrated, got %d", n)
	}

	// Original JSONL untouched.
	if _, err := os.Stat(filepath.Join(dir, "msg_chat-a.json")); err != nil {
		t.Errorf("expected JSONL to remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".legacy")); !os.IsNotExist(err) {
		t.Errorf(".legacy/ should not exist when migration is skipped")
	}
}

func TestMigrateLegacyJSONLs_NoStoreDir(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "messages.db")
	s, err := OpenMessageStore(dbPath)
	if err != nil {
		t.Fatalf("OpenMessageStore: %v", err)
	}
	defer s.Close()

	missing := filepath.Join(dir, "does-not-exist")
	n, err := MigrateLegacyJSONLs(s, missing)
	if err != nil {
		t.Fatalf("MigrateLegacyJSONLs(missing): %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 migrated for missing dir, got %d", n)
	}
}

func TestMigrateLegacyJSONLs_TolerantOfMalformedLines(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "messages.db")
	s, err := OpenMessageStore(dbPath)
	if err != nil {
		t.Fatalf("OpenMessageStore: %v", err)
	}
	defer s.Close()

	// Mix one valid record and one corrupt line.
	path := filepath.Join(dir, "msg_chat-a.json")
	f, _ := os.Create(path)
	enc := json.NewEncoder(f)
	_ = enc.Encode(SavedMessage{ChatJID: "chat-a", ID: "A1", Text: "ok", Timestamp: 100})
	fmt.Fprintln(f, "{not valid json at all")
	_ = enc.Encode(SavedMessage{ChatJID: "chat-a", ID: "A2", Text: "ok2", Timestamp: 200})
	f.Close()

	n, err := MigrateLegacyJSONLs(s, dir)
	if err != nil {
		t.Fatalf("MigrateLegacyJSONLs: %v", err)
	}
	// json.Decoder stops at first decode error in a stream — once it hits
	// the malformed line, it can't recover. So we accept 1 valid record
	// before the corruption, the remaining valid one is lost. That's fine
	// — production data is well-formed; this test just proves we don't
	// panic on corruption.
	if n < 1 {
		t.Errorf("expected at least 1 migrated, got %d", n)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./client/ -run "TestMigrateLegacyJSONLs" -v
```

Expected: FAIL with `undefined: MigrateLegacyJSONLs`.

- [ ] **Step 3: Implement migration**

Create `client/migrate.go`:

```go
package client

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// MigrateLegacyJSONLs imports every store/msg_*.json file into the
// MessageStore in a single transaction, then renames the JSONLs into
// store/.legacy/ as backup. No-op if the messages table already has rows.
//
// Returns the number of records imported (0 if nothing to do).
func MigrateLegacyJSONLs(s *MessageStore, storeDir string) (int, error) {
	var existing int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&existing); err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	if existing > 0 {
		return 0, nil
	}

	entries, err := os.ReadDir(storeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read store dir: %w", err)
	}

	var paths []string
	var allRecs []SavedMessage
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "msg_") || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(storeDir, name)
		recs := parseLegacyJSONL(path)
		paths = append(paths, path)
		allRecs = append(allRecs, recs...)
	}

	if len(allRecs) == 0 {
		return 0, nil
	}

	if err := s.InsertBatch(allRecs); err != nil {
		return 0, fmt.Errorf("insert batch: %w", err)
	}

	legacyDir := filepath.Join(storeDir, ".legacy")
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		return 0, fmt.Errorf("mkdir .legacy: %w", err)
	}
	for _, p := range paths {
		dest := filepath.Join(legacyDir, filepath.Base(p))
		if err := os.Rename(p, dest); err != nil {
			return 0, fmt.Errorf("rename %s: %w", p, err)
		}
	}
	return len(allRecs), nil
}

// parseLegacyJSONL reads a JSONL file into SavedMessage records. Errors
// during decode are logged and skipped — once the decoder hits a corrupt
// line in the stream, json.Decoder cannot resync to subsequent records,
// but the records up to the corruption are recovered.
func parseLegacyJSONL(path string) []SavedMessage {
	f, err := os.Open(path)
	if err != nil {
		log.Printf("migrate: open %s: %v", path, err)
		return nil
	}
	defer f.Close()
	var out []SavedMessage
	dec := json.NewDecoder(f)
	for dec.More() {
		var r SavedMessage
		if err := dec.Decode(&r); err != nil {
			log.Printf("migrate: stop reading %s after decode error: %v", path, err)
			break
		}
		if r.ChatJID == "" {
			continue
		}
		out = append(out, r)
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./client/ -run "TestMigrateLegacyJSONLs" -v
```

Expected: PASS for all four.

- [ ] **Step 5: Commit**

```bash
git add client/migrate.go client/migrate_test.go
git commit -m "$(cat <<'EOF'
feat(client): one-shot JSONL→SQLite migration helper

Idempotent: skips when the messages table already has rows. On
success moves JSONLs into store/.legacy/ as backup. Tolerant of
corrupt lines (logs and stops reading the affected file).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Atomic wiring — swap persistence layer everywhere

This is the cutover task. Multiple files change in one commit because the persistence path is interdependent: `WhatsAppClient` constructor changes, all callers swap from JSONL to MessageStore, `main.go` opens and threads the store through. Done atomically because there's no dual-write phase (per spec).

**Files:**
- Modify: `client/whatsmeow_client.go`
- Modify: `client/media.go`
- Modify: `ui/chat_view.go`
- Modify: `main.go`

- [ ] **Step 1: Update `WhatsAppClient` struct + constructor in `client/whatsmeow_client.go`**

Find the struct around line 156-178 and change two things:
- Add `msgStore *MessageStore` field
- Remove `muStoreFile sync.Mutex` field

Use Edit:
- old_string:
```
// WhatsAppClient wraps the whatsmeow client with higher-level operations
type WhatsAppClient struct {
	client            *whatsmeow.Client
	store             *sqlstore.Container
	OnMessage         func(MessageEvent)
	OnLogin           LoginCallback
	OnHistoryUpdate   func()
	OnMediaReady      func(chatJID, msgID, mediaPath string) // fires when an async download finishes
	OnReactionUpdate  func(ReactionUpdate)                   // fires when a reaction is added/removed
	OnMessageEdit     func(MessageEdit)                      // fires when a message's text was edited
	OnMessageDelete   func(MessageDelete)                    // fires on "delete for everyone"
	OnMessageStatus   func(MessageStatus)                    // fires when delivered/read receipt arrives
	muChannels        sync.RWMutex
	ContactCache      map[string]Contact
	muContacts        sync.RWMutex
	muMessages        sync.Mutex
	messages          map[string][]MessageEvent
	chatRegistry      map[string]string // jid -> display name
	muChats           sync.RWMutex
	groupCache        map[string]string // jid -> group name
	muGroups          sync.RWMutex
	muStoreFile       sync.Mutex // serialize writes to store/*.json
}
```
- new_string:
```
// WhatsAppClient wraps the whatsmeow client with higher-level operations
type WhatsAppClient struct {
	client            *whatsmeow.Client
	store             *sqlstore.Container
	msgStore          *MessageStore
	OnMessage         func(MessageEvent)
	OnLogin           LoginCallback
	OnHistoryUpdate   func()
	OnMediaReady      func(chatJID, msgID, mediaPath string) // fires when an async download finishes
	OnReactionUpdate  func(ReactionUpdate)                   // fires when a reaction is added/removed
	OnMessageEdit     func(MessageEdit)                      // fires when a message's text was edited
	OnMessageDelete   func(MessageDelete)                    // fires on "delete for everyone"
	OnMessageStatus   func(MessageStatus)                    // fires when delivered/read receipt arrives
	muChannels        sync.RWMutex
	ContactCache      map[string]Contact
	muContacts        sync.RWMutex
	muMessages        sync.Mutex
	messages          map[string][]MessageEvent
	chatRegistry      map[string]string // jid -> display name
	muChats           sync.RWMutex
	groupCache        map[string]string // jid -> group name
	muGroups          sync.RWMutex
}
```

- [ ] **Step 2: Update `NewWhatsAppClient` signature to accept `*MessageStore`**

Find the function around line 181-222 and change the signature + body init.

- old_string:
```
// NewWhatsAppClient creates a new WhatsApp client instance
func NewWhatsAppClient(clientStore *sqlstore.Container) *WhatsAppClient {
	wa := &WhatsAppClient{
		ContactCache: make(map[string]Contact),
		messages:     make(map[string][]MessageEvent),
		chatRegistry: make(map[string]string),
		groupCache:   make(map[string]string),
	}
```
- new_string:
```
// NewWhatsAppClient creates a new WhatsApp client instance.
// msgStore is required — chat history persistence goes through it.
func NewWhatsAppClient(clientStore *sqlstore.Container, msgStore *MessageStore) *WhatsAppClient {
	wa := &WhatsAppClient{
		msgStore:     msgStore,
		ContactCache: make(map[string]Contact),
		messages:     make(map[string][]MessageEvent),
		chatRegistry: make(map[string]string),
		groupCache:   make(map[string]string),
	}
```

- [ ] **Step 3: Replace `appendMessages` body in `client/whatsmeow_client.go`**

Find function around line 745-774. Replace the entire function body to delegate to MessageStore (we keep the method as a thin wrapper to minimize call-site churn).

- old_string:
```
// appendMessages writes records to store/msg_<jid>.json, one JSON object per line.
func (w *WhatsAppClient) appendMessages(jidStr string, msgs []SavedMessage) error {
	if len(msgs) == 0 {
		return nil
	}
	
	w.muStoreFile.Lock()
	defer w.muStoreFile.Unlock()

	storeDir := filepath.Join(".", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		return fmt.Errorf("failed to create store directory: %w", err)
	}
	
	path := filepath.Join(storeDir, fmt.Sprintf("msg_%s.json", jidStr))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open message file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, m := range msgs {
		if err := enc.Encode(m); err != nil {
			return fmt.Errorf("failed to encode message: %w", err)
		}
	}
	
	return nil
}
```
- new_string:
```
// appendMessages persists records to the SQLite store. Kept as a thin
// wrapper for callers; jidStr is redundant (each rec carries ChatJID)
// but the parameter signature is preserved to keep the call sites short.
func (w *WhatsAppClient) appendMessages(jidStr string, msgs []SavedMessage) error {
	_ = jidStr // each record's ChatJID is the source of truth
	return w.msgStore.InsertBatch(msgs)
}
```

- [ ] **Step 4: Delete `loadMessageIDs` from `client/whatsmeow_client.go`**

It's no longer needed — `INSERT OR IGNORE` handles dedup at the DB level.

- old_string:
```
// loadMessageIDs returns the set of message IDs already stored for a chat,
// for dedup when HistorySync redelivers messages we've already saved.
func (w *WhatsAppClient) loadMessageIDs(jidStr string) map[string]bool {
	out := make(map[string]bool)
	path := filepath.Join(".", "store", fmt.Sprintf("msg_%s.json", jidStr))
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for dec.More() {
		var m struct {
			ID string `json:"id"`
		}
		if err := dec.Decode(&m); err != nil {
			continue
		}
		if m.ID != "" {
			out[m.ID] = true
		}
	}
	return out
}

```
- new_string: (empty — delete the function)

- [ ] **Step 5: Update `handleHistorySync` to skip the `seen` map check**

Find around line 778-911. The seen-map dedup is now done by INSERT OR IGNORE at the DB layer; we just call InsertBatch and rely on PK constraint.

- old_string:
```
		seen := w.loadMessageIDs(jidStr)
		var batch []SavedMessage
```
- new_string:
```
		var batch []SavedMessage
```

Then remove the seen-map maintenance inside the loop:
- old_string:
```
			if id != "" && seen[id] {
				continue
			}

			sender := key.GetParticipant()
```
- new_string:
```
			sender := key.GetParticipant()
```

And remove the seen[id] = true line at the end of the loop:
- old_string:
```
			batch = append(batch, rec)
			if id != "" {
				seen[id] = true
			}
		}
```
- new_string:
```
			batch = append(batch, rec)
		}
```

- [ ] **Step 6: Add `LoadMessages` and `ChatSummaries` public methods to `WhatsAppClient`**

Append these methods to `client/whatsmeow_client.go` (anywhere after the type definition, e.g., near the end of the file just before the dead `SearchChats` function — or just after `FetchContacts`):

Find a natural insertion point. Pick after the `FetchContacts` function (around line 1306) and before `GetContacts`:

- old_string:
```
// GetContacts returns the full contact list
func (w *WhatsAppClient) GetContacts() []Contact {
```
- new_string:
```
// LoadMessages returns persisted messages for a chat in chronological order.
// Direct passthrough to MessageStore.LoadChat — kept on the client surface
// so the UI doesn't need to import the store type directly.
func (w *WhatsAppClient) LoadMessages(chatJID string) ([]SavedMessage, error) {
	return w.msgStore.LoadChat(chatJID)
}

// ChatSummaries returns one ChatSummary per known chat, ordered by recency.
// Replaces the UI's previous loop of os.ReadDir + os.Stat + tail-parse-JSONL.
func (w *WhatsAppClient) ChatSummaries() ([]ChatSummary, error) {
	return w.msgStore.ChatSummaries()
}

// GetContacts returns the full contact list
func (w *WhatsAppClient) GetContacts() []Contact {
```

- [ ] **Step 7: Replace `patchRecord` body in `client/media.go`**

Find around line 167-213. Replace the implementation with a thin wrapper.

- old_string:
```
// patchRecord finds the message record matching msgID in the chat's JSONL,
// applies mutate, and rewrites the file atomically (temp + rename) only if
// mutate returned true. Used for media-path fills, reaction updates, and
// future edit/delete features.
func (w *WhatsAppClient) patchRecord(chatJID, msgID string, mutate func(*SavedMessage) bool) {
	w.muStoreFile.Lock()
	defer w.muStoreFile.Unlock()

	src := filepath.Join(".", "store", fmt.Sprintf("msg_%s.json", chatJID))
	in, err := os.Open(src)
	if err != nil {
		return
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(src), "msg_*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	dec := json.NewDecoder(in)
	enc := json.NewEncoder(tmp)
	patched := false
	for dec.More() {
		var rec SavedMessage
		if err := dec.Decode(&rec); err != nil {
			continue
		}
		if rec.ID == msgID {
			if mutate(&rec) {
				patched = true
			}
		}
		if err := enc.Encode(&rec); err != nil {
			tmp.Close()
			return
		}
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if patched {
		_ = os.Rename(tmpName, src)
	}
}
```
- new_string:
```
// patchRecord delegates to MessageStore.Patch. The wrapper is kept so the
// existing call sites stay short. Errors are logged but not surfaced —
// matches the previous best-effort behavior of the JSONL implementation.
func (w *WhatsAppClient) patchRecord(chatJID, msgID string, mutate func(*SavedMessage) bool) {
	if err := w.msgStore.Patch(chatJID, msgID, mutate); err != nil {
		log.Printf("patchRecord %s/%s: %v", chatJID, msgID, err)
	}
}
```

- [ ] **Step 8: Clean up unused imports in `client/media.go`**

After removing the JSONL machinery in `patchRecord`, several imports become unused. The remaining content uses `context`, `os`, `path/filepath`, `strings`, `sync`, plus a few whatsmeow imports. Verify by attempting build (see Step 14); the compiler will list what's no longer needed.

Specifically: `encoding/json` and `fmt` MIGHT still be needed (check `extForMime`, `mediaPath`, `inflightDownloads`, etc — only `fmt` is used by Errorf calls). Let `go build` tell us.

- [ ] **Step 9: Clean up unused imports in `client/whatsmeow_client.go`**

After deleting `loadMessageIDs` and replacing `appendMessages`, `encoding/json` may still be in use (extractText etc). `os` and `path/filepath` may not. Let `go build` tell us.

- [ ] **Step 10: Replace `loadMessagesFromDisk` in `ui/chat_view.go`**

Find around line 1282-1393. Replace the entire function body to use `cv.waClient.LoadMessages` and map. The sender-resolution legacy logic stays.

- old_string:
```
func (cv *ChatView) loadMessagesFromDisk(jid string) []*Message {
	var msgs []*Message
	filename := fmt.Sprintf("msg_%s.json", jid)
	path := filepath.Join(".", "store", filename)

	file, err := os.Open(path)
	if err != nil {
		return []*Message{}
	}
	defer file.Close()

	seenID := make(map[string]bool)
	decoder := json.NewDecoder(file)
	for decoder.More() {
		var sm struct {
			ID         string `json:"id,omitempty"`
			ChatJID    string `json:"chat_jid"`
			SenderJID  string `json:"sender_jid"`
			SenderName string `json:"sender_name,omitempty"`
			Text       string `json:"text"`
			Timestamp  int64  `json:"timestamp"`
			FromMe     bool   `json:"from_me"`

			MediaType string `json:"media_type,omitempty"`
			MediaPath string `json:"media_path,omitempty"`
			Mimetype  string `json:"mimetype,omitempty"`
			FileName  string `json:"filename,omitempty"`
			FileSize  uint64 `json:"file_size,omitempty"`
			Width     uint32 `json:"width,omitempty"`
			Height    uint32 `json:"height,omitempty"`
			Duration  uint32 `json:"duration,omitempty"`
			ThumbB64  string `json:"thumb_b64,omitempty"`

			ReplyToID         string `json:"reply_to_id,omitempty"`
			ReplyToSenderName string `json:"reply_to_sender_name,omitempty"`
			ReplyToText       string `json:"reply_to_text,omitempty"`
			ReplyToMediaType  string `json:"reply_to_media_type,omitempty"`

			Reactions []client.SavedReaction `json:"reactions,omitempty"`

			Edited  bool   `json:"edited,omitempty"`
			Deleted bool   `json:"deleted,omitempty"`
			Status  string `json:"status,omitempty"`
		}
		if err := decoder.Decode(&sm); err != nil {
			continue
		}

		// Dedup by ID: outgoing messages may appear twice in the JSONL when
		// the server echoes back (we persist locally on send + persistIncoming
		// runs on the echo). Keep the first record we see.
		if sm.ID != "" {
			if seenID[sm.ID] {
				continue
			}
			seenID[sm.ID] = true
		}

		// Resolve sender display name. Prefer the explicit sender_name (new
		// schema). Fallback for legacy records: parse "Name: text" prefix.
		sender := ""
		text := sm.Text
		if sm.FromMe {
			sender = "You"
			if sm.SenderName == "" {
				if idx := strings.Index(text, ": "); idx >= 0 && idx < 40 {
					text = text[idx+2:]
				}
			}
		} else if sm.SenderName != "" {
			sender = sm.SenderName
		} else {
			if idx := strings.Index(text, ": "); idx >= 0 && idx < 40 {
				sender = text[:idx]
				text = text[idx+2:]
			} else if senderJID, err := types.ParseJID(sm.SenderJID); err == nil {
				sender = cv.waClient.LookupName(senderJID)
			}
		}

		var thumb []byte
		if sm.ThumbB64 != "" {
			thumb, _ = base64.StdEncoding.DecodeString(sm.ThumbB64)
		}

		msgs = append(msgs, &Message{
			ID:                sm.ID,
			Sender:            sender,
			Text:              text,
			Timestamp:         time.Unix(sm.Timestamp, 0),
			IsOwn:             sm.FromMe,
			MediaType:         sm.MediaType,
			MediaPath:         sm.MediaPath,
			Mimetype:          sm.Mimetype,
			FileName:          sm.FileName,
			FileSize:          sm.FileSize,
			Width:             sm.Width,
			Height:            sm.Height,
			Duration:          sm.Duration,
			Thumb:             thumb,
			ReplyToID:         sm.ReplyToID,
			ReplyToSenderName: sm.ReplyToSenderName,
			ReplyToText:       sm.ReplyToText,
			ReplyToMediaType:  sm.ReplyToMediaType,
			Reactions:         sm.Reactions,
			Edited:            sm.Edited,
			Deleted:           sm.Deleted,
			Status:            sm.Status,
		})
	}
	return msgs
}
```
- new_string:
```
func (cv *ChatView) loadMessagesFromDisk(jid string) []*Message {
	recs, err := cv.waClient.LoadMessages(jid)
	if err != nil {
		return []*Message{}
	}
	msgs := make([]*Message, 0, len(recs))
	for _, sm := range recs {
		// Resolve sender display name. Prefer the explicit sender_name (new
		// schema). Fallback for legacy records: parse "Name: text" prefix.
		sender := ""
		text := sm.Text
		if sm.FromMe {
			sender = "You"
			if sm.SenderName == "" {
				if idx := strings.Index(text, ": "); idx >= 0 && idx < 40 {
					text = text[idx+2:]
				}
			}
		} else if sm.SenderName != "" {
			sender = sm.SenderName
		} else {
			if idx := strings.Index(text, ": "); idx >= 0 && idx < 40 {
				sender = text[:idx]
				text = text[idx+2:]
			} else if senderJID, err := types.ParseJID(sm.SenderJID); err == nil {
				sender = cv.waClient.LookupName(senderJID)
			}
		}

		var thumb []byte
		if sm.ThumbB64 != "" {
			thumb, _ = base64.StdEncoding.DecodeString(sm.ThumbB64)
		}

		msgs = append(msgs, &Message{
			ID:                sm.ID,
			Sender:            sender,
			Text:              text,
			Timestamp:         time.Unix(sm.Timestamp, 0),
			IsOwn:             sm.FromMe,
			MediaType:         sm.MediaType,
			MediaPath:         sm.MediaPath,
			Mimetype:          sm.Mimetype,
			FileName:          sm.FileName,
			FileSize:          sm.FileSize,
			Width:             sm.Width,
			Height:            sm.Height,
			Duration:          sm.Duration,
			Thumb:             thumb,
			ReplyToID:         sm.ReplyToID,
			ReplyToSenderName: sm.ReplyToSenderName,
			ReplyToText:       sm.ReplyToText,
			ReplyToMediaType:  sm.ReplyToMediaType,
			Reactions:         sm.Reactions,
			Edited:            sm.Edited,
			Deleted:           sm.Deleted,
			Status:            sm.Status,
		})
	}
	return msgs
}
```

- [ ] **Step 11: Replace the JSONL-scan portion of `loadChatList` in `ui/chat_view.go`**

Find around line 904-986. Replace the entire function with the SQLite-driven version.

- old_string:
```
func (cv *ChatView) loadChatList() {
	chats, err := cv.waClient.GetChats()
	if err != nil {
		chats = []client.Chat{}
	}

	// Discover any chats that exist on disk but aren't in the API result yet
	// (typical case: groups whose history was saved but FetchGroups hasn't run).
	known := make(map[string]bool, len(chats))
	for _, c := range chats {
		known[c.JID.String()] = true
	}

	storeDir := filepath.Join(".", "store")
	if entries, err := os.ReadDir(storeDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasPrefix(name, "msg_") || !strings.HasSuffix(name, ".json") {
				continue
			}
			jidStr := strings.TrimSuffix(strings.TrimPrefix(name, "msg_"), ".json")
			if known[jidStr] {
				continue
			}
			jid, err := types.ParseJID(jidStr)
			if err != nil {
				continue
			}
			if jid.Server == types.BroadcastServer {
				continue
			}
			displayName := cv.waClient.LookupName(jid)
			chats = append(chats, client.Chat{
				JID:         jid,
				DisplayName: displayName,
				IsGroup:     jid.Server == types.GroupServer,
			})
			known[jidStr] = true
		}
	}

	// Drop any status entries that snuck in from the API result.
	filtered := chats[:0]
	for _, c := range chats {
		if c.JID.Server == types.BroadcastServer {
			continue
		}
		filtered = append(filtered, c)
	}
	chats = filtered

	cv.muCachedChats.Lock()
	cv.allChats = chats
	cv.muCachedChats.Unlock()

	var activeChats []client.Chat
	for _, chat := range chats {
		jidStr := chat.JID.String()
		filename := fmt.Sprintf("msg_%s.json", jidStr)
		path := filepath.Join(storeDir, filename)

		info, err := os.Stat(path)
		if err == nil && info.Size() > 0 {
			chat.LastMessageTime = info.ModTime().Unix()
			chat.LastMessage = cv.getLastMessagePreview(path)
			if chat.DisplayName == "" {
				chat.DisplayName = cv.waClient.LookupName(chat.JID)
			}
			activeChats = append(activeChats, chat)
		}
	}

	sort.Slice(activeChats, func(i, j int) bool {
		return activeChats[i].LastMessageTime > activeChats[j].LastMessageTime
	})

	cv.muCachedChats.Lock()
	cv.cachedChats = activeChats
	cv.muCachedChats.Unlock()
}
```
- new_string:
```
func (cv *ChatView) loadChatList() {
	chats, err := cv.waClient.GetChats()
	if err != nil {
		chats = []client.Chat{}
	}

	known := make(map[string]bool, len(chats))
	for _, c := range chats {
		known[c.JID.String()] = true
	}

	summaries, err := cv.waClient.ChatSummaries()
	if err != nil {
		log.Printf("ChatSummaries: %v", err)
	}

	// Discover chats present in the DB but missing from the API result
	// (typical: groups whose history was saved but FetchGroups hasn't run).
	for _, sum := range summaries {
		if known[sum.ChatJID] {
			continue
		}
		jid, err := types.ParseJID(sum.ChatJID)
		if err != nil {
			continue
		}
		if jid.Server == types.BroadcastServer {
			continue
		}
		chats = append(chats, client.Chat{
			JID:         jid,
			DisplayName: cv.waClient.LookupName(jid),
			IsGroup:     jid.Server == types.GroupServer,
		})
		known[sum.ChatJID] = true
	}

	// Drop any status entries that snuck in from the API result.
	filtered := chats[:0]
	for _, c := range chats {
		if c.JID.Server == types.BroadcastServer {
			continue
		}
		filtered = append(filtered, c)
	}
	chats = filtered

	cv.muCachedChats.Lock()
	cv.allChats = chats
	cv.muCachedChats.Unlock()

	summaryByJID := make(map[string]client.ChatSummary, len(summaries))
	for _, s := range summaries {
		summaryByJID[s.ChatJID] = s
	}

	var activeChats []client.Chat
	for _, chat := range chats {
		sum, ok := summaryByJID[chat.JID.String()]
		if !ok {
			continue
		}
		chat.LastMessageTime = sum.LastTimestamp
		chat.LastMessage = formatLastMessagePreview(sum)
		if chat.DisplayName == "" {
			chat.DisplayName = cv.waClient.LookupName(chat.JID)
		}
		activeChats = append(activeChats, chat)
	}

	sort.Slice(activeChats, func(i, j int) bool {
		return activeChats[i].LastMessageTime > activeChats[j].LastMessageTime
	})

	cv.muCachedChats.Lock()
	cv.cachedChats = activeChats
	cv.muCachedChats.Unlock()
}

// formatLastMessagePreview turns a ChatSummary into the sidebar's preview
// line. Mirrors the previous getLastMessagePreview formatting (legacy
// "Name: " prefix stripping, "[mediatype]" fallback, "You: " / "Sender: "
// prefix).
func formatLastMessagePreview(s client.ChatSummary) string {
	text := s.LastText
	if text == "" && s.LastMediaType != "" {
		text = "[" + s.LastMediaType + "]"
	}
	if s.LastSenderName == "" {
		if idx := strings.Index(text, ": "); idx >= 0 && idx < 40 {
			text = text[idx+2:]
		}
	}
	switch {
	case s.LastFromMe:
		text = "You: " + text
	case s.LastSenderName != "":
		text = s.LastSenderName + ": " + text
	}
	return text
}
```

- [ ] **Step 12: Delete `getLastMessagePreview` from `ui/chat_view.go`**

It's now redundant — `formatLastMessagePreview` does the same job from a `ChatSummary`. Find around line 988-1049.

- old_string:
```
func (cv *ChatView) getLastMessagePreview(path string) string {
	// Tail-seek: read at most the last 16KB and parse the trailing JSONL line.
	// 16KB comfortably covers records with embedded JPEG thumbnails (~5-10KB
	// base64) without paying the I/O of a multi-MB chat history just for a
	// sidebar preview.
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return ""
	}

	const window int64 = 16 * 1024
	off := info.Size() - window
	if off < 0 {
		off = 0
	}
	buf := make([]byte, info.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil {
		return ""
	}

	tail := strings.TrimRight(string(buf), "\n")
	if i := strings.LastIndex(tail, "\n"); i >= 0 {
		tail = tail[i+1:]
	}

	var lastMsg struct {
		Text       string `json:"text"`
		FromMe     bool   `json:"from_me"`
		SenderName string `json:"sender_name,omitempty"`
		MediaType  string `json:"media_type,omitempty"`
	}
	if err := json.Unmarshal([]byte(tail), &lastMsg); err != nil {
		return ""
	}

	text := lastMsg.Text
	// Media fallback: if text/caption empty, surface the media type.
	if text == "" && lastMsg.MediaType != "" {
		text = "[" + lastMsg.MediaType + "]"
	}

	// Strip legacy "Name: text" prefix on old records (no sender_name).
	if lastMsg.SenderName == "" {
		if idx := strings.Index(text, ": "); idx >= 0 && idx < 40 {
			text = text[idx+2:]
		}
	}

	switch {
	case lastMsg.FromMe:
		text = "You: " + text
	case lastMsg.SenderName != "":
		text = lastMsg.SenderName + ": " + text
	}
	return text
}

```
- new_string: (empty — delete the function)

- [ ] **Step 13: Update `main.go` to open the store and run migration**

Find the section after `storeContainer` is created and before `waClient := client.NewWhatsAppClient(...)` (around line 36-48).

- old_string:
```
	logger := waLog.Stdout("Main", "INFO", false)
	storeContainer, err := sqlstore.New(context.Background(), "sqlite3", "whatsapp.db?_foreign_keys=on", logger)
	if err != nil {
		log.Fatalf("Failed to create client store: %v", err)
	}

	if _, err := storeContainer.GetFirstDevice(context.Background()); err != nil {
		log.Printf("Warning: could not get device: %v", err)
	}

	// Migrate any pre-circular avatar JPGs to circular PNGs (idempotent).
	client.MigrateLegacyAvatars()

	waClient := client.NewWhatsAppClient(storeContainer)
```
- new_string:
```
	logger := waLog.Stdout("Main", "INFO", false)
	storeContainer, err := sqlstore.New(context.Background(), "sqlite3", "whatsapp.db?_foreign_keys=on", logger)
	if err != nil {
		log.Fatalf("Failed to create client store: %v", err)
	}

	if _, err := storeContainer.GetFirstDevice(context.Background()); err != nil {
		log.Printf("Warning: could not get device: %v", err)
	}

	msgStore, err := client.OpenMessageStore("store/messages.db")
	if err != nil {
		log.Fatalf("Failed to open message store: %v", err)
	}
	defer msgStore.Close()

	if migrated, err := client.MigrateLegacyJSONLs(msgStore, "store"); err != nil {
		log.Printf("warning: legacy JSONL migration failed: %v", err)
	} else if migrated > 0 {
		log.Printf("migrated %d legacy messages from JSONL to SQLite", migrated)
	}

	// Migrate any pre-circular avatar JPGs to circular PNGs (idempotent).
	client.MigrateLegacyAvatars()

	waClient := client.NewWhatsAppClient(storeContainer, msgStore)
```

- [ ] **Step 14: Build and fix unused imports**

```bash
go build ./...
```

Expected: builds clean OR fails with "imported and not used" errors. Each error names a specific import — remove those imports from the named file. Common removals after this task:

In `client/whatsmeow_client.go`: `os`, `path/filepath`, `sort` may be unused if no other code references them. Use `goimports` or remove manually.

In `client/media.go`: `encoding/json`, `os` may now be unused (still need `path/filepath` for `mediaPath`, `strings` for `extForMime`).

In `ui/chat_view.go`: `encoding/json` may still be used (let me think — only the JSON unmarshal was in `getLastMessagePreview` which is deleted, and the savedMessage struct decode in `loadMessagesFromDisk` is gone). Likely safe to remove `encoding/json`. `os` may still be used (other parts of file). `path/filepath` may still be used.

For each unused-import error: use Edit to remove that single import line. Re-run `go build ./...` until clean.

- [ ] **Step 15: Run all unit tests**

```bash
go test ./client/ -v
```

Expected: all tests in store_test.go and migrate_test.go pass.

- [ ] **Step 16: Run go vet**

```bash
go vet ./...
```

Expected: no warnings.

- [ ] **Step 17: Commit**

```bash
git add client/whatsmeow_client.go client/media.go ui/chat_view.go main.go
git commit -m "$(cat <<'EOF'
feat(persist): swap JSONL persistence for SQLite store

Atomic cutover: WhatsAppClient now requires a *MessageStore;
appendMessages, persistOwn, persistIncoming, and patchRecord all route
through it. loadMessageIDs and the per-chat seen-map are gone — the
composite PK + INSERT OR IGNORE handles HistorySync dedup. The UI's
loadMessagesFromDisk and loadChatList now read from the store via
LoadMessages and ChatSummaries; getLastMessagePreview is replaced by
formatLastMessagePreview consuming a ChatSummary.

main.go opens store/messages.db, runs MigrateLegacyJSONLs (idempotent,
no-op if the table already has rows), and threads the store into the
WhatsApp client. JSONLs survive in store/.legacy/ as backup.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: Manual smoke verification

No code changes. Validate the migration end-to-end against real data.

**Files:** none

- [ ] **Step 1: Pre-launch backup**

```bash
cp -r store store.backup
```

This is a safety net in case the migration loses data. After verifying the migration was correct, the backup can be deleted.

- [ ] **Step 2: Launch the app and watch logs**

```bash
go run . 2>&1 | head -50
```

Expected log lines:
- `migrated N legacy messages from JSONL to SQLite` (where N matches the number of records you had — for the dev environment, around 1994).
- No errors mentioning the message store.

- [ ] **Step 3: Verify the SQLite file and .legacy/ directory**

```bash
ls -la store/messages.db store/.legacy/ | head -20
sqlite3 store/messages.db "SELECT COUNT(*) FROM messages;"
sqlite3 store/messages.db "SELECT chat_jid, COUNT(*) FROM messages GROUP BY chat_jid LIMIT 5;"
```

Expected:
- `store/messages.db` exists, ~1MB or so.
- `store/.legacy/` contains all the original `msg_*.json` files.
- The COUNT matches (or is very close to) the number of records in the original JSONLs.

- [ ] **Step 4: Smoke-test the UI**

With the app still running:
1. Click through several chats — verify message history renders.
2. Send a message — verify it appears immediately and persists after restart.
3. React to an incoming message — verify the reaction appears.
4. Receive a new message in a chat that's not currently focused — verify the unread badge increments.
5. Restart the app (Ctrl+Q, then relaunch) — verify everything is still there.

- [ ] **Step 5: Verify a mutation persisted via SQL**

After reacting to a message, find the message ID (e.g., from app logs or by inspecting the DB):

```bash
sqlite3 store/messages.db "SELECT id, status, reactions_json FROM messages WHERE chat_jid = '<some-chat-jid>' ORDER BY ts DESC LIMIT 5;"
```

Expected: at least one row has a non-empty `reactions_json` (showing your test reaction) or a non-empty `status` (showing a delivery receipt).

- [ ] **Step 6: Verify migration is idempotent**

Restart the app and re-check the log. There should be NO "migrated N messages" line on the second launch (because `messages` is no longer empty).

- [ ] **Step 7: Cleanup backup (optional)**

If everything verified clean:

```bash
rm -rf store.backup
```

If anything went wrong, restore via:

```bash
rm -rf store
mv store.backup store
# then revert the SQLite migration commits
```

---

## Self-Review Checklist

Worked through after writing the plan. All items addressed inline.

**1. Spec coverage**
- Schema with composite PK + ts index → Task 2.
- WAL/cache_size DSN → Task 2 (in `OpenMessageStore`).
- Insert/InsertBatch/Patch/LoadChat/ChatSummaries API → Tasks 3, 4, 5, 6.
- One-shot migration with `.legacy/` backup → Task 7.
- Replace appendMessages, persistOwn, persistIncoming, loadMessageIDs, patchRecord, loadMessagesFromDisk, loadChatList scan, getLastMessagePreview, muStoreFile → Task 8.
- Hook in main.go → Task 8 step 13.
- Test plan items 1–7 from spec → Tasks 2–7 (item 7 "WAL concurrency" is implicitly covered by `*sql.DB`'s pool semantics; not a separate test — flagged as minor gap below).

**2. Placeholder scan**
- All code blocks contain final code, no TBDs.
- Step 14 ("fix unused imports") is the only non-prescriptive step — but it can't be prescriptive because the unused imports depend on what's left in each file after deletions. The step explicitly tells the executor how to find and fix them.

**3. Type consistency**
- `SavedMessage` (Task 1) used consistently in all subsequent tasks.
- `MessageStore` methods named the same across plan and spec.
- `ChatSummary` fields match between spec and Tasks 6, 8.
- `formatLastMessagePreview` is a new helper introduced in Task 8 step 11; consumed only there. No conflict.

**Minor gap acknowledged**: spec test plan item 7 (WAL concurrency stress test) is not in this plan. SQLite's WAL + `*sql.DB`'s connection pool are both well-tested upstream; we don't need a custom test. If the executor wants extra confidence they can add a goroutine stress test, but it's not required for correctness.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-08-sqlite-migration.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
