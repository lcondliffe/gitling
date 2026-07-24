//go:build !sqlite

package cache

import (
	"encoding/gob"
	"os"
	"path/filepath"

	"github.com/lcondliffe/gitling/internal/aggregate"
)

// Store is the default, dependency-free cache backend: a single gob file per
// date basis (aggregates-author.gob / aggregates-commit.gob), so switching
// --date can never read the other basis's stale data (see cache.Backend).
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

// New returns the default Backend rooted at the given git directory, scoped
// to basis: a gob file store. Build with `-tags sqlite` to get a
// sqlite-backed Backend instead (see store_sqlite.go).
func New(gitDir string, basis aggregate.DateBasis) Backend {
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
