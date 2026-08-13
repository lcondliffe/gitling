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

// TestRunPRsGateSkipsTheForgeCLI covers the wiring the forge package can't:
// that --prs actually decides whether the CLI is run at all, and that the
// panel reaches the dashboard when it is. The stub `gh` touches a marker file,
// so "was it executed" is a file that exists or doesn't.
func TestRunPRsGateSkipsTheForgeCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stub isn't executable on Windows")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH; skipping integration test")
	}

	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
		{"remote", "add", "origin", "https://github.com/acme/thing.git"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}

	marker := filepath.Join(t.TempDir(), "gh-ran")
	bin := t.TempDir()
	stub := "#!/bin/sh\ntouch " + marker + `
printf '%s' '[{"number":42,"title":"feat: add the widget","author":{"login":"ada"},"isDraft":false,"updatedAt":"2024-06-15T10:00:00Z"}]'`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(repo)

	opts := options{view: "dashboard", bucket: "day", dateBasis: aggregate.AuthorDate, layout: "stack"}

	opts.prs = true
	var buf bytes.Buffer
	if err := run(&buf, opts); err != nil {
		t.Fatalf("run(prs on): %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("gh should have run: %v", err)
	}
	if !strings.Contains(buf.String(), "OPEN PRS") {
		t.Errorf("dashboard should show the panel:\n%s", buf.String())
	}

	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	opts.prs = false
	buf.Reset()
	if err := run(&buf, opts); err != nil {
		t.Fatalf("run(prs off): %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("gh should not have run with --prs=false (stat err: %v)", err)
	}
	if strings.Contains(buf.String(), "OPEN PRS") {
		t.Errorf("panel should be absent with --prs=false:\n%s", buf.String())
	}
}
