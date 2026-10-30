package gitdata

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCommitsScope(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q")
	if err := os.MkdirAll(filepath.Join(dir, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	commitFile(t, dir, "api/a.go", "a", "api", time.Time{})
	commitFile(t, dir, "web.js", "w", "web", time.Time{})
	writeRepoFile(t, dir, "api/a.go", "a2")
	writeRepoFile(t, dir, "api/deps.lock", "l")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "mixed")
	commitFile(t, dir, "api/deps.lock", "l2", "lock only", time.Time{})
	writeRepoFile(t, dir, "api/a.go", "a3")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "--author", "Bot <bot@example.com>", "-m", "bot")

	// Opened from a subdirectory: paths still resolve from the repo root.
	r, err := Open(filepath.Join(dir, "api"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Commits("", Scope{Paths: []string{"api"}, ExcludePaths: []string{"*.lock"}, ExcludeAuthors: []string{"BOT@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	// "mixed" keeps only api/a.go; "web", "lock only" and the bot drop out.
	if len(got) != 2 {
		t.Fatalf("got %d commits, want 2: %+v", len(got), got)
	}
	for _, c := range got {
		if len(c.Files) != 1 || c.Files[0] != "api/a.go" {
			t.Errorf("commit %s files = %v, want [api/a.go]", c.Hash, c.Files)
		}
	}

	// Excludes alone cover the whole repo, not just the cwd.
	got, err = r.Commits("", Scope{ExcludePaths: []string{"*.lock"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Errorf("exclude-only: got %d commits, want 4", len(got))
	}
}
