// Package cache persists derived aggregates so repeat runs only process
// commits newer than the last one seen.
//
// The store is a single gob file per date basis under
// <git-dir>/gitling-cache/. gob keeps gitling dependency-free and the write
// path trivial, and because the file lives inside the git directory it is
// already ignored by git.
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

// Store is the cache: a single gob file per date basis
// (aggregates-author.gob / aggregates-commit.gob), so switching --date can
// never read the other basis's stale data.
//
// The cache stores commits already bucketed by day (see aggregate.Merge), so
// an author-bucketed payload and a commit-bucketed payload are not
// interchangeable: reusing one for the other basis would silently produce
// wrong day totals. Storage is scoped to the requested basis (a per-basis
// file) and the basis is stamped into the payload as a second line of
// defense.
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
// crash mid-write cannot corrupt an existing cache. The temp file gets a unique
// name so two gitling runs on the same repo can't truncate each other's
// half-written cache and rename the wreckage into place.
func (s *Store) Save(agg *aggregate.Aggregates, lastHash string) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, filepath.Base(s.path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err := gob.NewEncoder(f).Encode(payload{Version: version, LastHash: lastHash, Basis: s.basis, Agg: *agg}); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
