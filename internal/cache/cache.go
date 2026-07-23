// Package cache persists derived aggregates so repeat runs only process commits
// newer than the last one seen.
//
// The store is a single gob file under <git-dir>/gitling-cache/. gob is used
// over sqlite to keep gitling dependency-free and the write path trivial;
// because the file lives inside the git directory it is already ignored by git.
// The package depends only on aggregate (the value it serializes), keeping it
// cleanly swappable for a sqlite-backed store later.
package cache

import (
	"encoding/gob"
	"os"
	"path/filepath"

	"github.com/lcondliffe/gitling/internal/aggregate"
)

const (
	dirName = "gitling-cache"
	version = 3 // bump to invalidate on incompatible schema changes
)

// Store reads and writes the aggregate cache for one repository and date
// basis.
//
// The cache stores commits already bucketed by day (see aggregate.Merge), so
// an author-bucketed payload and a commit-bucketed payload are not
// interchangeable: reusing one for the other basis would silently produce
// wrong day totals. To keep them from mixing, each basis gets its own cache
// file (aggregates-author.gob / aggregates-commit.gob), so switching --date
// simply misses the other basis's file and rebuilds instead of reading stale
// data. The basis is also stamped into the payload and re-checked on Load as
// a second line of defense (e.g. against a hand-edited or copied cache file).
type Store struct {
	path  string
	basis aggregate.DateBasis
}

type payload struct {
	Version  int
	LastHash string // HEAD when this cache was written
	Basis    aggregate.DateBasis
	Agg      aggregate.Aggregates
}

// New returns a Store rooted at the given git directory, scoped to basis.
func New(gitDir string, basis aggregate.DateBasis) *Store {
	fileName := "aggregates-" + string(basis) + ".gob"
	return &Store{path: filepath.Join(gitDir, dirName, fileName), basis: basis}
}

// Load returns the cached aggregates and the HEAD hash they were built from. ok
// is false on any miss (absent, unreadable, version mismatch, or a basis that
// doesn't match this Store's); callers should then rebuild from full history.
func (s *Store) Load() (agg *aggregate.Aggregates, lastHash string, ok bool) {
	f, err := os.Open(s.path)
	if err != nil {
		return nil, "", false
	}
	defer f.Close()

	var p payload
	if err := gob.NewDecoder(f).Decode(&p); err != nil || p.Version != version || p.Basis != s.basis {
		return nil, "", false
	}
	return &p.Agg, p.LastHash, true
}

// Save writes aggregates and the HEAD hash atomically (temp file + rename) so a
// crash mid-write cannot corrupt an existing cache.
func (s *Store) Save(agg *aggregate.Aggregates, lastHash string) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := gob.NewEncoder(f).Encode(payload{Version: version, LastHash: lastHash, Basis: s.basis, Agg: *agg}); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.path)
}
