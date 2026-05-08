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
