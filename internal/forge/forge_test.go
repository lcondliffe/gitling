package forge

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseGH(t *testing.T) {
	data := []byte(`[
	  {"number":42,"title":"feat: add the widget","author":{"login":"ada"},"isDraft":false,"updatedAt":"2024-06-15T10:00:00Z"},
	  {"number":41,"title":"wip","author":{"login":"alan"},"isDraft":true,"updatedAt":"2024-06-14T09:00:00Z"}
	]`)

	prs, err := parseGH(data)
	if err != nil {
		t.Fatalf("parseGH: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("got %d PRs, want 2: %+v", len(prs), prs)
	}
	want := PR{
		Number:  42,
		Title:   "feat: add the widget",
		Author:  "ada",
		Updated: time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC),
	}
	if prs[0] != want {
		t.Errorf("prs[0] = %+v, want %+v", prs[0], want)
	}
	if !prs[1].Draft {
		t.Errorf("prs[1] should be a draft: %+v", prs[1])
	}
}

// A remote no configured forge recognises never runs a command.
func TestListSkipsUnknownHost(t *testing.T) {
	if prs := List(".", "git@bitbucket.org:acme/thing.git", 5); prs != nil {
		t.Errorf("got %+v, want nil", prs)
	}
}

// stubGH puts an executable `gh` on PATH for the duration of the test, and
// nothing else: the shell script body decides what the fake CLI does. PATH
// holds only that directory, so the body has to stick to shell builtins.
func stubGH(t *testing.T, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stub isn't executable on Windows")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestListRunsTheForgeCLI(t *testing.T) {
	// The stub also records its arguments, so the limit really reaches the CLI.
	args := filepath.Join(t.TempDir(), "args")
	stubGH(t, `echo "$@" > `+args+`
printf '%s' '[{"number":42,"title":"feat: add the widget","author":{"login":"ada"},"isDraft":false,"updatedAt":"2024-06-15T10:00:00Z"}]'`)

	prs := List(t.TempDir(), "https://github.com/acme/thing.git", 5)
	if len(prs) != 1 || prs[0].Number != 42 {
		t.Fatalf("got %+v, want one PR #42", prs)
	}
	recorded, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(recorded), "--limit 5") {
		t.Errorf("limit not passed to the CLI: %q", recorded)
	}
}

// The forge CLI is an optional dependency: gitling has to survive it being
// absent, broken, or logged out, since the panel is a bonus and the rest of the
// dashboard doesn't depend on it.
func TestListToleratesUnusableCLI(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string // empty == no gh on PATH at all
	}{
		{name: "missing"},
		{name: "not authenticated", body: `echo "gh: not logged in" >&2; exit 1`},
		{name: "garbage output", body: `echo "not json"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.body == "" {
				t.Setenv("PATH", t.TempDir()) // empty dir: nothing to find
			} else {
				stubGH(t, tc.body)
			}
			if prs := List(t.TempDir(), "https://github.com/acme/thing.git", 5); prs != nil {
				t.Errorf("got %+v, want nil", prs)
			}
		})
	}
}
