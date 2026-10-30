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

	// User-written magic is merged with ours, from the root and a subdirectory.
	for _, d := range []string{dir, filepath.Join(dir, "api")} {
		r, err := Open(d)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name  string
			scope Scope
			want  int
		}{
			{"long include", Scope{Paths: []string{":(icase)WEB.JS"}}, 1},
			{"long exclude", Scope{ExcludePaths: []string{":(glob)**/*.lock"}}, 4},
			{"short exclude", Scope{Paths: []string{"api", ":!api/deps.lock"}}, 3},
			{"short top", Scope{Paths: []string{":/web.js"}}, 1},
		} {
			got, err := r.Commits("", tc.scope)
			if err != nil {
				t.Fatalf("%s from %s: %v", tc.name, d, err)
			}
			if len(got) != tc.want {
				t.Errorf("%s from %s: got %d commits, want %d", tc.name, d, len(got), tc.want)
			}
		}
	}
}

func TestAnchor(t *testing.T) {
	for _, tc := range []struct {
		in      string
		exclude bool
		want    string
	}{
		{"api", false, ":(top)api"},
		{"*.lock", true, ":(top,exclude)*.lock"},
		{":(icase)readme.md", false, ":(icase,top)readme.md"},
		{":(glob)**/*.lock", true, ":(glob,top,exclude)**/*.lock"},
		{":(top,exclude)x", true, ":(top,exclude)x"},
		{":!x", false, ":(top,exclude)x"},
		{":/x", false, ":(top)x"},
		{":/!:x", false, ":(top,exclude)x"},
	} {
		if got := anchor(tc.in, tc.exclude); got != tc.want {
			t.Errorf("anchor(%q, %v) = %q, want %q", tc.in, tc.exclude, got, tc.want)
		}
	}
}
