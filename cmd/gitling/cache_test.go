package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/lcondliffe/gitling/internal/aggregate"
)

// The cache-invalidation branches in run() decide whether history is walked
// fully, incrementally, or not at all. A wrong choice double-counts commits or
// serves stale totals, so each case asserts the resulting commit total.

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

// newRepo returns an initialised repository with n commits.
func newRepo(t *testing.T, commits int) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH; skipping integration test")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test")
	for i := 0; i < commits; i++ {
		git(t, dir, "commit", "-q", "--allow-empty", "-m", "c")
	}
	return dir
}

// totalCommits runs the dashboard in JSON and returns the aggregate's total.
func totalCommits(t *testing.T, dir string) int {
	t.Helper()
	t.Chdir(dir)
	var buf bytes.Buffer
	o := options{view: "dashboard", bucket: "day", dateBasis: aggregate.AuthorDate, layout: "stack", json: true}
	if err := run(&buf, o); err != nil {
		t.Fatalf("run: %v", err)
	}
	var m struct {
		Activity struct {
			TotalCommits int `json:"total_commits"`
		} `json:"activity"`
	}
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v\n%s", err, buf.String())
	}
	return m.Activity.TotalCommits
}

func TestRunCachePaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		// setup returns the repo, already warmed (or not) as the case needs.
		setup func(t *testing.T) string
		want  int
	}{
		{
			name:  "cold cache walks full history",
			setup: func(t *testing.T) string { return newRepo(t, 3) },
			want:  3,
		},
		{
			name: "warm cache with no new commits",
			setup: func(t *testing.T) string {
				dir := newRepo(t, 3)
				totalCommits(t, dir) // warms the cache at HEAD
				return dir
			},
			want: 3,
		},
		{
			name: "warm cache plus new commits walks only the new ones",
			setup: func(t *testing.T) string {
				dir := newRepo(t, 3)
				totalCommits(t, dir)
				git(t, dir, "commit", "-q", "--allow-empty", "-m", "new")
				return dir
			},
			want: 4,
		},
		{
			name: "rewritten history rebuilds instead of double-counting",
			setup: func(t *testing.T) string {
				dir := newRepo(t, 3)
				totalCommits(t, dir)
				git(t, dir, "commit", "-q", "--amend", "--allow-empty", "-m", "amended")
				return dir
			},
			want: 3,
		},
		{
			name: "cached history whose HEAD went away reports nothing",
			setup: func(t *testing.T) string {
				dir := newRepo(t, 3)
				totalCommits(t, dir)
				git(t, dir, "update-ref", "-d", "HEAD")
				return dir
			},
			want: 0,
		},
		{
			name:  "empty repo has no HEAD to walk",
			setup: func(t *testing.T) string { return newRepo(t, 0) },
			want:  0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.setup(t)
			if got := totalCommits(t, dir); got != tc.want {
				t.Errorf("total commits = %d, want %d", got, tc.want)
			}
		})
	}
}
