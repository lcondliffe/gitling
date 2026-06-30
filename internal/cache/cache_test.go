package cache

import (
	"encoding/gob"
	"os"
	"testing"

	"github.com/lcondliffe/gitling/internal/aggregate"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

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
	s := New(t.TempDir())
	if _, _, ok := s.Load(); ok {
		t.Error("Load on missing cache returned ok = true")
	}
}

func TestLoadVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	// Hand-write a payload with a stale version.
	if err := os.MkdirAll(dirFor(s), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := gob.NewEncoder(f).Encode(payload{Version: version - 1, LastHash: "x"}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if _, _, ok := s.Load(); ok {
		t.Error("Load with stale version returned ok = true, want false (forces rebuild)")
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
