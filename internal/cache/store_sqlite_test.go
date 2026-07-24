//go:build sqlite

package cache

import (
	"database/sql"
	"strconv"
	"testing"

	"github.com/lcondliffe/gitling/internal/aggregate"
)

func TestSQLiteSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, aggregate.AuthorDate).(*SQLiteStore)

	agg := aggregate.New()
	agg.Days["2024-06-02"] = aggregate.DayBucket{
		Commits: 3, Insertions: 10, Deletions: 2,
		Authors: map[string]int{"a@x": 3},
		Files:   map[string]int{"f.go": 3},
	}
	agg.AuthorNames["a@x"] = "Alice"

	if err := s.Save(agg, "deadbeef"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, hash, ok := s.Load()
	if !ok {
		t.Fatal("Load: ok = false after Save")
	}
	if hash != "deadbeef" {
		t.Errorf("hash = %q, want deadbeef", hash)
	}
	b := got.Days["2024-06-02"]
	if b.Commits != 3 || b.Insertions != 10 || b.Authors["a@x"] != 3 || b.Files["f.go"] != 3 {
		t.Errorf("round-tripped bucket = %+v", b)
	}
	if got.AuthorNames["a@x"] != "Alice" {
		t.Errorf("AuthorNames not preserved: %v", got.AuthorNames)
	}
}

func TestSQLiteLoadMissing(t *testing.T) {
	s := New(t.TempDir(), aggregate.AuthorDate).(*SQLiteStore)
	if _, _, ok := s.Load(); ok {
		t.Error("Load on missing cache returned ok = true")
	}
}

func TestSQLiteLoadVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, aggregate.AuthorDate).(*SQLiteStore)

	// Save a valid cache, then hand-write a stale version into meta.
	agg := aggregate.New()
	if err := s.Save(agg, "x"); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE meta SET value = ? WHERE key = 'version'`, strconv.Itoa(version-1)); err != nil {
		t.Fatal(err)
	}

	if _, _, ok := s.Load(); ok {
		t.Error("Load with stale version returned ok = true, want false (forces rebuild)")
	}
}

func TestSQLiteBasesDoNotMix(t *testing.T) {
	dir := t.TempDir()
	author := New(dir, aggregate.AuthorDate).(*SQLiteStore)
	commit := New(dir, aggregate.CommitDate).(*SQLiteStore)

	if author.path == commit.path {
		t.Fatalf("author and commit stores share a path: %s", author.path)
	}

	agg := aggregate.New()
	agg.Days["2024-06-02"] = aggregate.DayBucket{Commits: 1, Authors: map[string]int{"a@x": 1}, Files: map[string]int{"f.go": 1}}
	if err := author.Save(agg, "abc123"); err != nil {
		t.Fatal(err)
	}

	if _, _, ok := commit.Load(); ok {
		t.Error("commit-basis Load found the author-basis cache; bases must not mix")
	}
	if _, _, ok := author.Load(); !ok {
		t.Error("author-basis Load missed its own cache")
	}
}
