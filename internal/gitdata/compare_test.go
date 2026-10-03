package gitdata

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseNumstatZ(t *testing.T) {
	out := "3\t1\tplain.go\x00-\t-\tlogo.png\x00" +
		"2\t0\t\x00old name.txt\x00new\tname.txt\x00" +
		"1\t0\tline\nbreak\x00"
	want := []FileChange{
		{Path: "plain.go", Insertions: 3, Deletions: 1},
		{Path: "logo.png", Binary: true},
		{Path: "new\tname.txt", OldPath: "old name.txt", Insertions: 2},
		{Path: "line\nbreak", Insertions: 1},
	}
	if got := parseNumstatZ(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseNumstatZ:\n got %#v\nwant %#v", got, want)
	}
}

func TestCompare(t *testing.T) {
	f := newFixture(t)
	gitCmd(t, f.local, "checkout", "-q", "-b", "feature")

	names := []string{"with space.txt", "-leading-dash.txt", "ünïcødé.txt"}
	if runtime.GOOS != "windows" {
		names = append(names, "tab\there.txt", "new\nline.txt")
	}
	for _, n := range names {
		writeRepoFile(t, f.local, n, "a\nb\n")
		gitCmd(t, f.local, "add", "--", n)
		gitCmd(t, f.local, "commit", "-q", "-m", "add "+n)
	}
	writeRepoFile(t, f.local, "blob.bin", "\x00\x01\x02")
	gitCmd(t, f.local, "add", "blob.bin")
	gitCmd(t, f.local, "mv", "README.md", "README.txt")
	gitCmd(t, f.local, "commit", "-q", "-m", "binary and rename")

	// Work landing on main after the branch point must not count as the
	// branch's change (three-dot diff) nor its commits (two-dot log).
	gitCmd(t, f.local, "checkout", "-q", "main")
	commitFile(t, f.local, "main-only.txt", "x\n", "main moves on", time.Time{})
	gitCmd(t, f.local, "checkout", "-q", "feature")

	c, err := f.repo(t).Compare("main", 2)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if c.CommitCount != len(names)+1 || len(c.Commits) != 2 {
		t.Errorf("commits: count %d listed %d, want %d and 2", c.CommitCount, len(c.Commits), len(names)+1)
	}
	if c.Commits[0].Subject != "binary and rename" {
		t.Errorf("newest commit = %q", c.Commits[0].Subject)
	}
	if c.MergeBase == c.BaseOID {
		t.Error("merge base equals base tip; main should have moved on")
	}

	got := map[string]FileChange{}
	for _, fc := range c.Files {
		got[fc.Path] = fc
	}
	if len(got) != len(names)+2 {
		t.Errorf("files = %v, want %d", c.Files, len(names)+2)
	}
	for _, n := range names {
		if fc := got[n]; fc.Insertions != 2 {
			t.Errorf("%q = %+v, want 2 insertions", n, fc)
		}
	}
	if !got["blob.bin"].Binary {
		t.Error("blob.bin not reported binary")
	}
	if got["README.txt"].OldPath != "README.md" {
		t.Errorf("rename = %+v", got["README.txt"])
	}
	if _, ok := got["main-only.txt"]; ok {
		t.Error("main-only change leaked into the branch diff")
	}
	if c.Insertions != 2*len(names) || c.Deletions != 0 {
		t.Errorf("totals +%d -%d", c.Insertions, c.Deletions)
	}

	// No origin/HEAD in the fixture, so the default base is local main.
	c, err = f.repo(t).Compare("", 1)
	if err != nil || c.Base != "main" {
		t.Errorf("default base = %q, %v", c.Base, err)
	}
}

func TestCompareUnavailableBase(t *testing.T) {
	f := newFixture(t)
	r := f.repo(t)
	for _, base := range []string{"no-such-branch", "-p", "HEAD:README.md"} {
		if _, err := r.Compare(base, 5); err == nil {
			t.Errorf("Compare(%q) = nil error", base)
		}
	}

	gitCmd(t, f.local, "checkout", "-q", "--orphan", "island")
	commitFile(t, f.local, "other.txt", "x\n", "unrelated", time.Time{})
	if _, err := r.Compare("main", 5); err == nil || !strings.Contains(err.Error(), "share no history") {
		t.Errorf("unrelated histories: %v", err)
	}

	// No origin/HEAD and no main or master: nothing to default to.
	lone := t.TempDir()
	gitCmd(t, lone, "init", "-q", "-b", "trunk")
	commitFile(t, lone, "a.txt", "a\n", "init", time.Time{})
	lr, err := Open(lone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lr.Compare("", 5); err == nil || !strings.Contains(err.Error(), "--base") {
		t.Errorf("no default branch: %v", err)
	}
}

func TestCompareShallow(t *testing.T) {
	f := newFixture(t)
	commitFile(t, f.local, "b.txt", "b\n", "second", time.Time{})
	gitCmd(t, f.local, "push", "-q", "origin", "main")
	gitCmd(t, f.local, "push", "-q", "origin", "HEAD~1:refs/heads/old")

	root := t.TempDir()
	clone := filepath.Join(root, "shallow")
	gitCmd(t, root, "clone", "-q", "--no-local", "--depth=1", "--no-single-branch", f.remote, clone)
	r, err := Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	// Both tips are present but the history joining them was cut off.
	if _, err := r.Compare("origin/old", 5); err == nil || !strings.Contains(err.Error(), "shallow") {
		t.Errorf("shallow: %v", err)
	}
}

// diff.relative would otherwise hide changes outside the directory gitling runs in.
func TestCompareFromSubdirIgnoresDiffRelative(t *testing.T) {
	f := newFixture(t)
	gitCmd(t, f.local, "checkout", "-q", "-b", "feature")
	if err := os.Mkdir(filepath.Join(f.local, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	commitFile(t, f.local, "outside.txt", "x\n", "outside", time.Time{})
	commitFile(t, f.local, "sub/in.txt", "y\n", "inside", time.Time{})
	gitCmd(t, f.local, "config", "diff.relative", "true")

	r, err := Open(filepath.Join(f.local, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.Compare("main", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Files) != 2 || c.Insertions != 2 {
		t.Fatalf("files %+v, +%d; want outside.txt and sub/in.txt, +2", c.Files, c.Insertions)
	}
	for _, fc := range c.Files {
		if fc.Path != "outside.txt" && fc.Path != "sub/in.txt" {
			t.Errorf("unexpected path %q", fc.Path)
		}
	}
}
