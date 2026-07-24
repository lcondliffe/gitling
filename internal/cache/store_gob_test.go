//go:build !sqlite

package cache

import (
	"encoding/gob"
	"os"
	"testing"

	"github.com/lcondliffe/gitling/internal/aggregate"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, aggregate.AuthorDate).(*Store)

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

func TestLoadMissing(t *testing.T) {
	s := New(t.TempDir(), aggregate.AuthorDate).(*Store)
	if _, _, ok := s.Load(); ok {
		t.Error("Load on missing cache returned ok = true")
	}
}

func TestLoadVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, aggregate.AuthorDate).(*Store)
	// Hand-write a payload with a stale version.
	if err := os.MkdirAll(dirFor(s), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := gob.NewEncoder(f).Encode(payload{Version: version - 1, LastHash: "x", Basis: aggregate.AuthorDate}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if _, _, ok := s.Load(); ok {
		t.Error("Load with stale version returned ok = true, want false (forces rebuild)")
	}
}

// TestBasesDoNotMix verifies the CRITICAL cache-correctness requirement: an
// author-basis Store and a commit-basis Store rooted at the same git dir must
// use separate files and never read each other's cached payload, even if one
// is hand-crafted to claim the other's basis in its Version field.
func TestBasesDoNotMix(t *testing.T) {
	dir := t.TempDir()
	authorStore := New(dir, aggregate.AuthorDate).(*Store)
	commitStore := New(dir, aggregate.CommitDate).(*Store)

	if authorStore.path == commitStore.path {
		t.Fatalf("author and commit stores share a path: %s", authorStore.path)
	}

	authorAgg := aggregate.New()
	authorAgg.Days["2024-06-02"] = aggregate.DayBucket{Commits: 1}
	if err := authorStore.Save(authorAgg, "hash-a"); err != nil {
		t.Fatalf("Save (author): %v", err)
	}

	// The commit-basis store shares the same git dir but must not see the
	// author-basis payload at all: different file, and Load would also reject
	// it on a Basis mismatch even if it read the same file.
	if _, _, ok := commitStore.Load(); ok {
		t.Error("commit-basis Load found the author-basis payload, want miss")
	}

	commitAgg := aggregate.New()
	commitAgg.Days["2024-06-05"] = aggregate.DayBucket{Commits: 1}
	if err := commitStore.Save(commitAgg, "hash-c"); err != nil {
		t.Fatalf("Save (commit): %v", err)
	}

	gotAuthor, hashA, ok := authorStore.Load()
	if !ok || hashA != "hash-a" || gotAuthor.Days["2024-06-02"].Commits != 1 {
		t.Errorf("author store corrupted after commit store write: agg=%+v hash=%q ok=%v", gotAuthor, hashA, ok)
	}
	gotCommit, hashC, ok := commitStore.Load()
	if !ok || hashC != "hash-c" || gotCommit.Days["2024-06-05"].Commits != 1 {
		t.Errorf("commit store = %+v hash=%q ok=%v, want its own payload intact", gotCommit, hashC, ok)
	}
}

// dirFor returns the directory portion of the store's path.
func dirFor(s *Store) string {
	for i := len(s.path) - 1; i >= 0; i-- {
		if s.path[i] == os.PathSeparator {
			return s.path[:i]
		}
	}
	return "."
}
