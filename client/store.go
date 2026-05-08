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
