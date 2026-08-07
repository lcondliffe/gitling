package gitdata

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Operation kinds reported by Operation.Kind. These name the multi-step git
// operations that leave a repository in a half-finished state you can be
// interrupted in the middle of — the thing you most need to know on walking
// back into a repo.
const (
	OpNone       = ""
	OpRebase     = "rebase"
	OpAm         = "am"
	OpCherryPick = "cherry-pick"
	OpRevert     = "revert"
	OpMerge      = "merge"
	OpBisect     = "bisect"
)

// Operation is an in-progress multi-step git operation. Step/Total are the
// position in the sequence (e.g. commit 3 of 7 of a rebase) and are only
// populated for the operations git tracks a count for; Total 0 means unknown.
type Operation struct {
	Kind  string
	Step  int
	Total int
}

// InProgress reports whether the repository is part-way through an operation.
func (o Operation) InProgress() bool { return o.Kind != OpNone }

// detectOperation reports which multi-step operation, if any, the repository is
// part-way through, by looking for the state files git writes into the git dir
// for the duration. This is the same set of markers git's own prompt and status
// helpers key off, and reading them is cheaper (and backend-independent) than
// asking git.
//
// gitDir must be the *worktree's* git dir rather than the common dir: these
// files are per-worktree, so a rebase in one linked worktree must not show up
// as a rebase in another.
func detectOperation(gitDir string) Operation {
	if gitDir == "" {
		return Operation{}
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(gitDir, name))
		return err == nil
	}

	// Order matters: a rebase stopped on a conflict also leaves the sequencer
	// and (for some backends) MERGE_HEAD behind, so the outermost operation has
	// to win or the report names the wrong thing.
	switch {
	case exists("rebase-merge"):
		return Operation{Kind: OpRebase, Step: readInt(gitDir, "rebase-merge/msgnum"), Total: readInt(gitDir, "rebase-merge/end")}
	case exists("rebase-apply"):
		// The rebase-apply directory is shared between `git rebase --apply` and
		// `git am`; the "applying" marker distinguishes them.
		kind := OpRebase
		if exists("rebase-apply/applying") {
			kind = OpAm
		}
		return Operation{Kind: kind, Step: readInt(gitDir, "rebase-apply/next"), Total: readInt(gitDir, "rebase-apply/last")}
	case exists("CHERRY_PICK_HEAD"):
		return Operation{Kind: OpCherryPick}
	case exists("REVERT_HEAD"):
		return Operation{Kind: OpRevert}
	case exists("MERGE_HEAD"):
		return Operation{Kind: OpMerge}
	case exists("BISECT_LOG"):
		return Operation{Kind: OpBisect}
	default:
		return Operation{}
	}
}

// readInt reads a small integer out of a git state file, returning 0 when the
// file is missing or unparseable. These files hold a single decimal number.
func readInt(dir, name string) int {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return n
}

// lastFetch returns when the repository last fetched from a remote, taken from
// the mtime of FETCH_HEAD, which git rewrites on every fetch (and every pull).
// The zero time means it has never fetched — including a freshly cloned repo,
// since clone does not write FETCH_HEAD.
//
// commonDir must be the repository's common dir: FETCH_HEAD is shared by all
// linked worktrees rather than written per-worktree.
//
// This is what makes the ahead/behind counts readable: they are measured
// against remote-tracking refs that are exactly as fresh as the last fetch, so
// "↑0 ↓0" means something quite different an hour after a fetch than a week
// after one.
func lastFetch(commonDir string) time.Time {
	if commonDir == "" {
		return time.Time{}
	}
	fi, err := os.Stat(filepath.Join(commonDir, "FETCH_HEAD"))
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}
