package client

import (
	"database/sql"
	"encoding/json"
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

const selectOneSQL = `SELECT chat_jid, id, sender_jid, sender_name, text, ts, from_me,
    media_type, media_path, mimetype, filename, file_size, width, height, duration, thumb_b64,
    reply_to_id, reply_to_sender_jid, reply_to_sender_name, reply_to_text, reply_to_media_type,
    reactions_json, edited, edited_at, deleted, deleted_at, status
    FROM messages WHERE chat_jid = ? AND id = ?`

// LoadMessage returns a single persisted message. ok=false means the record
// is not present.
func (s *MessageStore) LoadMessage(chatJID, msgID string) (rec SavedMessage, ok bool, err error) {
	rec, err = scanMessage(s.db.QueryRow(selectOneSQL, chatJID, msgID))
	if err == sql.ErrNoRows {
		return SavedMessage{}, false, nil
	}
	if err != nil {
		return SavedMessage{}, false, err
	}
	return rec, true, nil
}

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
