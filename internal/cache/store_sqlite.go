//go:build sqlite

// This file implements the opt-in sqlite-backed Backend, built only with
// `-tags sqlite`. It uses modernc.org/sqlite, a pure-Go (cgo-free) driver, so
// cross-compilation (as done by the release workflow) keeps working without a
// C toolchain. The default, non-tagged build does not import this file or the
// driver, so `go build ./cmd/gitling` (no tags) stays dependency-free.
//
// Schema (normalized, to support future partial/range queries without
// deserializing the whole history):
//
//	meta(key TEXT PRIMARY KEY, value TEXT)
//	  - "version"   -> schema version, as text
//	  - "last_hash" -> HEAD hash the cache was built from
//	days(day TEXT PRIMARY KEY, commits INTEGER, insertions INTEGER,
//	     deletions INTEGER, authors_json TEXT, files_json TEXT)
//	  - one row per calendar day (matching aggregate.Aggregates.Days keys);
//	    the per-author and per-file tallies are stored as small JSON blobs
//	    rather than further-normalized tables, which is enough to answer any
//	    --since range by summing the day rows in range (as aggregate already
//	    does) while keeping the schema and the Save path simple.
//	author_names(email TEXT PRIMARY KEY, name TEXT)
//	  - the global email -> display name map
//
// Save replaces the whole cache within a single transaction (delete + insert),
// which is atomic from sqlite's point of view: a crash mid-write leaves the
// previous committed state intact.
package cache

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	_ "modernc.org/sqlite"

	"github.com/lcondliffe/gitling/internal/aggregate"
)

const sqliteFileName = "aggregates.db"

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS days (
	day         TEXT PRIMARY KEY,
	commits     INTEGER NOT NULL,
	insertions  INTEGER NOT NULL,
	deletions   INTEGER NOT NULL,
	authors_json TEXT NOT NULL,
	files_json   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS author_names (
	email TEXT PRIMARY KEY,
	name  TEXT NOT NULL
);
`

// SQLiteStore is the opt-in cache backend built with `-tags sqlite`.
type SQLiteStore struct {
	path string
}

// New returns a Backend rooted at the given git directory. Built with `-tags
// sqlite`, this returns the sqlite-backed store; without the tag, New (in
// store_gob.go) returns the default gob store instead.
func New(gitDir string) Backend {
	return &SQLiteStore{path: filepath.Join(gitDir, dirName, sqliteFileName)}
}

func (s *SQLiteStore) open() (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(sqliteSchema); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Load returns the cached aggregates and the HEAD hash they were built from. ok
// is false on any miss (absent, unreadable, or version mismatch); callers should
// then rebuild from full history.
func (s *SQLiteStore) Load() (agg *aggregate.Aggregates, lastHash string, ok bool) {
	db, err := s.open()
	if err != nil {
		return nil, "", false
	}
	defer db.Close()

	var versionStr string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'version'`).Scan(&versionStr); err != nil {
		return nil, "", false
	}
	gotVersion, err := strconv.Atoi(versionStr)
	if err != nil || gotVersion != version {
		return nil, "", false
	}
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'last_hash'`).Scan(&lastHash); err != nil {
		return nil, "", false
	}

	a := aggregate.New()

	dayRows, err := db.Query(`SELECT day, commits, insertions, deletions, authors_json, files_json FROM days`)
	if err != nil {
		return nil, "", false
	}
	defer dayRows.Close()
	for dayRows.Next() {
		var day, authorsJSON, filesJSON string
		var b aggregate.DayBucket
		if err := dayRows.Scan(&day, &b.Commits, &b.Insertions, &b.Deletions, &authorsJSON, &filesJSON); err != nil {
			return nil, "", false
		}
		if err := json.Unmarshal([]byte(authorsJSON), &b.Authors); err != nil {
			return nil, "", false
		}
		if err := json.Unmarshal([]byte(filesJSON), &b.Files); err != nil {
			return nil, "", false
		}
		a.Days[day] = b
	}
	if err := dayRows.Err(); err != nil {
		return nil, "", false
	}

	nameRows, err := db.Query(`SELECT email, name FROM author_names`)
	if err != nil {
		return nil, "", false
	}
	defer nameRows.Close()
	for nameRows.Next() {
		var email, name string
		if err := nameRows.Scan(&email, &name); err != nil {
			return nil, "", false
		}
		a.AuthorNames[email] = name
	}
	if err := nameRows.Err(); err != nil {
		return nil, "", false
	}

	return a, lastHash, true
}

// Save writes aggregates and the HEAD hash, replacing the previous cache
// contents inside a single transaction so a crash mid-write leaves the
// prior committed state intact.
func (s *SQLiteStore) Save(agg *aggregate.Aggregates, lastHash string) error {
	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit

	if _, err := tx.Exec(`DELETE FROM days`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM author_names`); err != nil {
		return err
	}

	insertDay, err := tx.Prepare(`INSERT INTO days (day, commits, insertions, deletions, authors_json, files_json) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insertDay.Close()

	for day, b := range agg.Days {
		authorsJSON, err := json.Marshal(b.Authors)
		if err != nil {
			return err
		}
		filesJSON, err := json.Marshal(b.Files)
		if err != nil {
			return err
		}
		if _, err := insertDay.Exec(day, b.Commits, b.Insertions, b.Deletions, string(authorsJSON), string(filesJSON)); err != nil {
			return fmt.Errorf("insert day %s: %w", day, err)
		}
	}

	insertName, err := tx.Prepare(`INSERT INTO author_names (email, name) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer insertName.Close()

	for email, name := range agg.AuthorNames {
		if _, err := insertName.Exec(email, name); err != nil {
			return fmt.Errorf("insert author %s: %w", email, err)
		}
	}

	if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('version', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, strconv.Itoa(version)); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('last_hash', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, lastHash); err != nil {
		return err
	}

	return tx.Commit()
}
