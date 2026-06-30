// Package gitdata is the git interaction layer for gitling.
//
// v0.1 shells out to the `git` binary: for log aggregation and the cheap
// working-tree queries this is both simpler and faster than a pure-Go walk,
// and the prompt explicitly allows it. The package is deliberately small and
// behind plain methods on Repo so a go-git backend could be slotted in later
// without touching the aggregate/cache/render layers.
package gitdata

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Field/record separators used in the git log pretty-format. These are ASCII
// control chars that never appear in commit metadata, so parsing is robust to
// commit messages, names, and paths containing arbitrary text.
const (
	recordSep = "\x1e" // between commit records
	unitSep   = "\x1f" // between header fields
)

// Commit is a single non-merge commit with its diff stats.
//
// Times come from %at/%ct (unix seconds) so there is no timezone parsing; we
// bucket on AuthorTime by default, per the prompt.
type Commit struct {
	Hash        string
	AuthorName  string
	AuthorEmail string
	AuthorTime  time.Time
	CommitTime  time.Time
	Insertions  int
	Deletions   int
	Files       []string // distinct paths touched (post-rename names)
}

// Vitals captures the current branch / working-tree state. These reflect "now"
// and are intentionally not cached.
type Vitals struct {
	Branch      string
	Detached    bool
	HasUpstream bool
	Ahead       int
	Behind      int
	DirtyFiles  int
	StashCount  int
	BranchCount int
}

// Repo is a handle to a git repository, identified by any path inside its
// working tree.
type Repo struct {
	dir string
}

// Open verifies dir is inside a git work tree and returns a Repo.
func Open(dir string) (*Repo, error) {
	r := &Repo{dir: dir}
	if _, err := r.run("rev-parse", "--is-inside-work-tree"); err != nil {
		return nil, fmt.Errorf("not a git repository (or no git on PATH): %w", err)
	}
	return r, nil
}

func (r *Repo) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// GitDir returns the absolute path to the repository's git directory (handles
// worktrees and submodules where .git may be a file).
func (r *Repo) GitDir() (string, error) {
	out, err := r.run("rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Head returns the current HEAD commit hash. Returns an error on an empty repo.
func (r *Repo) Head() (string, error) {
	out, err := r.run("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// IsAncestor reports whether maybeAncestor is an ancestor of descendant. It is
// used to validate a cached commit against the current HEAD before doing an
// incremental update; a false result (rewritten history, missing object) tells
// the caller to rebuild from scratch.
func (r *Repo) IsAncestor(maybeAncestor, descendant string) bool {
	if maybeAncestor == "" {
		return false
	}
	_, err := r.run("merge-base", "--is-ancestor", maybeAncestor, descendant)
	return err == nil
}

// Vitals gathers the current branch / tracking / working-tree state.
func (r *Repo) Vitals() (Vitals, error) {
	var v Vitals

	if out, err := r.run("symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		v.Branch = strings.TrimSpace(out)
	} else {
		v.Detached = true
		if out, err := r.run("rev-parse", "--short", "HEAD"); err == nil {
			v.Branch = strings.TrimSpace(out)
		} else {
			v.Branch = "(no commits)"
		}
	}

	// "<behind>\t<ahead>" relative to the upstream; errors when no upstream.
	if out, err := r.run("rev-list", "--left-right", "--count", "@{upstream}...HEAD"); err == nil {
		if f := strings.Fields(out); len(f) == 2 {
			v.HasUpstream = true
			v.Behind, _ = strconv.Atoi(f[0])
			v.Ahead, _ = strconv.Atoi(f[1])
		}
	}

	if out, err := r.run("status", "--porcelain"); err == nil {
		v.DirtyFiles = countLines(out)
	}
	if out, err := r.run("stash", "list"); err == nil {
		v.StashCount = countLines(out)
	}
	if out, err := r.run("for-each-ref", "--format=%(refname)", "refs/heads"); err == nil {
		v.BranchCount = countLines(out)
	}

	return v, nil
}

// Commits returns non-merge commits in revRange (e.g. "abc123..HEAD"), or the
// entire history when revRange is empty. Results carry numstat-derived file
// lists and insertion/deletion totals.
func (r *Repo) Commits(revRange string) ([]Commit, error) {
	// %aN/%aE are mailmap-resolved, so a .mailmap collapses split identities.
	format := "%x1e%H%x1f%aN%x1f%aE%x1f%at%x1f%ct"
	args := []string{"log", "--no-merges", "--numstat", "--pretty=format:" + format}
	if revRange != "" {
		args = append(args, revRange)
	}
	out, err := r.run(args...)
	if err != nil {
		return nil, err
	}
	return parseLog(out), nil
}

func parseLog(out string) []Commit {
	var commits []Commit
	for _, rec := range strings.Split(out, recordSep) {
		rec = strings.Trim(rec, "\n")
		if rec == "" {
			continue
		}
		lines := strings.Split(rec, "\n")
		fields := strings.Split(lines[0], unitSep)
		if len(fields) < 5 {
			continue
		}
		c := Commit{
			Hash:        fields[0],
			AuthorName:  fields[1],
			AuthorEmail: fields[2],
		}
		c.AuthorTime, _ = parseUnix(fields[3])
		c.CommitTime, _ = parseUnix(fields[4])

		for _, l := range lines[1:] {
			if strings.TrimSpace(l) == "" {
				continue
			}
			add, del, path, ok := parseNumstat(l)
			if !ok {
				continue
			}
			c.Insertions += add
			c.Deletions += del
			if path != "" {
				c.Files = append(c.Files, path)
			}
		}
		commits = append(commits, c)
	}
	return commits
}

// parseNumstat parses one `<added>\t<deleted>\t<path>` line. Binary files use
// "-" for the counts; renames are normalized to their new path.
func parseNumstat(line string) (add, del int, path string, ok bool) {
	parts := strings.SplitN(line, "\t", 3)
	if len(parts) < 3 {
		return 0, 0, "", false
	}
	if parts[0] != "-" {
		add, _ = strconv.Atoi(parts[0])
	}
	if parts[1] != "-" {
		del, _ = strconv.Atoi(parts[1])
	}
	return add, del, cleanPath(parts[2]), true
}

// cleanPath resolves the new name out of numstat rename notation, handling both
// "old => new" and "pre/{old => new}/post" forms. Unrecognized input is
// returned trimmed.
func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	if !strings.Contains(p, "=>") {
		return p
	}
	if i := strings.Index(p, "{"); i >= 0 {
		if j := strings.Index(p, "}"); j > i {
			inner := p[i+1 : j]
			newPart := inner
			if k := strings.Index(inner, "=>"); k >= 0 {
				newPart = strings.TrimSpace(inner[k+2:])
			}
			p = strings.ReplaceAll(p[:i]+newPart+p[j+1:], "//", "/")
			return strings.TrimSpace(p)
		}
	}
	if k := strings.Index(p, "=>"); k >= 0 {
		return strings.TrimSpace(p[k+2:])
	}
	return p
}

func parseUnix(s string) (time.Time, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(n, 0), nil
}

func countLines(s string) int {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
