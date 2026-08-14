package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lcondliffe/gitling/internal/aggregate"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

// TestRunReposOverview covers the multi-repo path end to end: discovery of
// child repos (and only repos), per-repo status, the PR count via a stubbed
// gh, and that --fetch is rejected inside a single repository.
func TestRunReposOverview(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stub isn't executable on Windows")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH; skipping integration test")
	}

	parent := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(parent, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		gitIn(t, dir, "init", "-q")
		gitIn(t, dir, "config", "user.email", "test@example.com")
		gitIn(t, dir, "config", "user.name", "Test")
		gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	}
	// beta is dirty and has a GitHub remote for the stubbed PR count.
	if err := os.WriteFile(filepath.Join(parent, "beta", "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, filepath.Join(parent, "beta"), "remote", "add", "origin", "https://github.com/acme/beta.git")
	// A plain subdirectory must not appear as a row.
	if err := os.Mkdir(filepath.Join(parent, "notrepo"), 0o755); err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	stub := `#!/bin/sh
printf '%s' '[{"number":7,"title":"t","author":{"login":"ada"},"isDraft":false,"updatedAt":"2024-06-15T10:00:00Z"}]'`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(parent)

	opts := options{view: "dashboard", bucket: "day", dateBasis: aggregate.AuthorDate, layout: "auto", prs: true}
	var buf bytes.Buffer
	if err := run(&buf, opts); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"2 repos", "alpha", "beta", "clean", "1 dirty", "1 PR"} {
		if !strings.Contains(out, want) {
			t.Errorf("overview should contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "notrepo") {
		t.Errorf("plain directory listed as a repo:\n%s", out)
	}

	// --json has no overview shape yet; asking for it is an error, not silence.
	opts.json = true
	if err := run(&buf, opts); err == nil || !strings.Contains(err.Error(), "--json") {
		t.Errorf("run(json): want --json error, got %v", err)
	}
	opts.json = false

	// Inside a single repo --fetch means nothing; it must be rejected.
	t.Chdir(filepath.Join(parent, "alpha"))
	opts.fetch = true
	if err := run(&buf, opts); err == nil || !strings.Contains(err.Error(), "--fetch") {
		t.Errorf("run(fetch in repo): want --fetch error, got %v", err)
	}
}
