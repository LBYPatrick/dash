package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sqlite "modernc.org/sqlite"
)

var errIndexCorrupt = errors.New("corrupt index")

func isCorruptIndex(err error) bool {
	var failure *sqlite.Error
	// SQLite primary result codes: CORRUPT=11, NOTADB=26. Busy, permissions,
	// disk-full, canceled work and I/O failures must never erase a healthy cache.
	return errors.Is(err, errIndexCorrupt) || (errors.As(err, &failure) && (failure.Code()&255 == 11 || failure.Code()&255 == 26))
}
func configureIndexStorage(db *sql.DB) error {
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return err
	}
	if strings.ToLower(mode) != "wal" {
		return fmt.Errorf("index requires WAL journaling, got %s", mode)
	}
	for _, setting := range []string{"PRAGMA synchronous=FULL", "PRAGMA busy_timeout=5000", "PRAGMA cache_size=-4096", "PRAGMA wal_autocheckpoint=256"} {
		if _, err := db.Exec(setting); err != nil {
			return err
		}
	}
	return nil
}

// SQLite recovers its own hot journals on open. Only a confirmed corruption
// quarantines the database, after closing it; keep one diagnostic copy, with
// sidecars together. Never discard a WAL merely because a process was killed.
func quarantineIndex(path string) error {
	directory := filepath.Join(filepath.Dir(path), ".index-corrupt")
	if err := os.RemoveAll(directory); err != nil {
		return err
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		return err
	}
	// Move the main file last, so a restart never creates a new database beside
	// the previous database's unhandled journal.
	for _, suffix := range []string{"-wal", "-shm", "-journal", ""} {
		source := path + suffix
		if err := os.Rename(source, filepath.Join(directory, filepath.Base(source))); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return syncDirectory(filepath.Dir(path))
}
