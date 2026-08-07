package gitdata

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeState creates the git state files (relative paths) inside a fresh temp
// git dir and returns its path. A path ending in "/" is created as a directory.
func writeState(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestDetectOperation(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  Operation
	}{
		{
			name:  "clean",
			files: map[string]string{"HEAD": "ref: refs/heads/main\n"},
			want:  Operation{},
		},
		{
			name:  "interactive rebase with position",
			files: map[string]string{"rebase-merge/msgnum": "3\n", "rebase-merge/end": "7\n"},
			want:  Operation{Kind: OpRebase, Step: 3, Total: 7},
		},
		{
			name:  "apply-backend rebase",
			files: map[string]string{"rebase-apply/next": "2\n", "rebase-apply/last": "5\n"},
			want:  Operation{Kind: OpRebase, Step: 2, Total: 5},
		},
		{
			// rebase-apply is shared with `git am`; the marker file decides.
			name:  "am",
			files: map[string]string{"rebase-apply/next": "1\n", "rebase-apply/last": "4\n", "rebase-apply/applying": ""},
			want:  Operation{Kind: OpAm, Step: 1, Total: 4},
		},
		{
			name:  "cherry-pick",
			files: map[string]string{"CHERRY_PICK_HEAD": "abc123\n"},
			want:  Operation{Kind: OpCherryPick},
		},
		{
			name:  "revert",
			files: map[string]string{"REVERT_HEAD": "abc123\n"},
			want:  Operation{Kind: OpRevert},
		},
		{
			name:  "merge",
			files: map[string]string{"MERGE_HEAD": "abc123\n"},
			want:  Operation{Kind: OpMerge},
		},
		{
			name:  "bisect",
			files: map[string]string{"BISECT_LOG": "git bisect start\n"},
			want:  Operation{Kind: OpBisect},
		},
		{
			// A rebase stopped on a conflict can leave MERGE_HEAD behind too;
			// the outer operation has to win or we name the wrong one.
			name:  "rebase outranks merge",
			files: map[string]string{"rebase-merge/msgnum": "1\n", "rebase-merge/end": "1\n", "MERGE_HEAD": "abc123\n"},
			want:  Operation{Kind: OpRebase, Step: 1, Total: 1},
		},
		{
			// Some git versions omit the counters; the operation is still real.
			name:  "rebase without counters",
			files: map[string]string{"rebase-merge/head-name": "refs/heads/feature\n"},
			want:  Operation{Kind: OpRebase},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := detectOperation(writeState(t, c.files))
			if got != c.want {
				t.Errorf("detectOperation = %+v, want %+v", got, c.want)
			}
			if got.InProgress() != (c.want.Kind != OpNone) {
				t.Errorf("InProgress() = %v for %+v", got.InProgress(), got)
			}
		})
	}
}

func TestDetectOperationMissingDir(t *testing.T) {
	if got := detectOperation(""); got.InProgress() {
		t.Errorf("detectOperation(\"\") = %+v, want none", got)
	}
	if got := detectOperation(filepath.Join(t.TempDir(), "nope")); got.InProgress() {
		t.Errorf("detectOperation(missing dir) = %+v, want none", got)
	}
}

func TestLastFetch(t *testing.T) {
	dir := writeState(t, map[string]string{"FETCH_HEAD": "abc123\tbranch 'main' of example.com\n"})
	want := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(dir, "FETCH_HEAD"), want, want); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if got := lastFetch(dir); !got.Equal(want) {
		t.Errorf("lastFetch = %v, want %v", got, want)
	}
}

// A repo that has never fetched (including a fresh clone, which does not write
// FETCH_HEAD) reports the zero time so callers can omit the field entirely.
func TestLastFetchNeverFetched(t *testing.T) {
	if got := lastFetch(t.TempDir()); !got.IsZero() {
		t.Errorf("lastFetch = %v, want zero time", got)
	}
	if got := lastFetch(""); !got.IsZero() {
		t.Errorf("lastFetch(\"\") = %v, want zero time", got)
	}
}
