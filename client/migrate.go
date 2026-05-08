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
