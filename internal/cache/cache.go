// Package cache persists derived aggregates so repeat runs only process commits
// newer than the last one seen.
//
// The default store is a single gob file under <git-dir>/gitling-cache/. gob is
// used over sqlite to keep gitling dependency-free and the write path trivial,
// and because the file lives inside the git directory it is already ignored by
// git. An opt-in sqlite-backed store is available behind the `sqlite` build tag
// (see store_sqlite.go) for very large repos or future partial/range queries;
// build with `-tags sqlite` to use it. Both backends implement Backend, so
// callers are agnostic to which one New returns.
package cache

import (
	"github.com/lcondliffe/gitling/internal/aggregate"
)

const (
	dirName = "gitling-cache"
	version = 3 // bump to invalidate on incompatible schema changes
)

// Backend reads and writes the aggregate cache for one repository and date
// basis.
//
// The cache stores commits already bucketed by day (see aggregate.Merge), so
// an author-bucketed payload and a commit-bucketed payload are not
// interchangeable: reusing one for the other basis would silently produce
// wrong day totals. Both the gob store (default) and the sqlite store
// (opt-in, `-tags sqlite`) guard against this by scoping storage to the
// requested basis (e.g. a per-basis file) and by stamping the basis into the
// payload as a second line of defense; see store_gob.go / store_sqlite.go.
type Backend interface {
	// Load returns the cached aggregates and the HEAD hash they were built
	// from. ok is false on any miss (absent, unreadable, version mismatch,
	// or a basis that doesn't match what was requested); callers should then
	// rebuild from full history.
	Load() (agg *aggregate.Aggregates, lastHash string, ok bool)

	// Save writes aggregates and the HEAD hash, atomically where possible, so
	// a crash mid-write cannot corrupt an existing cache.
	Save(agg *aggregate.Aggregates, lastHash string) error
}
