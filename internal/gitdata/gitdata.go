// Package gitdata is the git interaction layer for gitling.
//
// The default build shells out to the `git` binary: for log aggregation and
// the cheap working-tree queries this is both simpler and faster than a pure-
// Go walk. The git interaction surface is captured in the Backend interface;
// Repo (this file's public type) is a thin dispatcher over whichever Backend
// was selected, so the aggregate/cache/render layers never need to know which
// one is in use.
//
// An optional pure-Go go-git backend is available behind the `gogit` build
// tag (see gogit.go, backend_gogit.go); without that tag the default build
// stays dependency-free and only the shell-out backend in this file
// (shellRepo) is compiled in.
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

// Branch is one local branch's overview state for the branches drill-down.
// Ahead/Behind are only meaningful when HasCompare is true; CompareRef names
// what they are measured against (the branch's upstream, or the default branch
// as a fallback for branches with no upstream configured).
type Branch struct {
	Name       string
	IsHead     bool      // the currently checked-out branch
	Upstream   string    // tracking ref (short), empty when none is configured
	Gone       bool      // upstream configured but no longer exists
	Ahead      int       // commits on this branch not on CompareRef
	Behind     int       // commits on CompareRef not on this branch
	HasCompare bool      // whether Ahead/Behind (and CompareRef) are populated
	CompareRef string    // upstream or fallback base branch
	LastCommit time.Time // committer date of the branch tip
	LastAuthor string    // author name of the branch tip
}

// shellRepo is the default Backend implementation: a handle to a git
// repository, identified by any path inside its working tree, that shells
// out to the `git` binary for every operation.
type shellRepo struct {
	dir string
}

// openShell verifies dir is inside a git work tree and returns a shellRepo.
func openShell(dir string) (*shellRepo, error) {
	r := &shellRepo{dir: dir}
	if _, err := r.run("rev-parse", "--is-inside-work-tree"); err != nil {
		return nil, fmt.Errorf("not a git repository (or no git on PATH): %w", err)
	}
	return r, nil
}

func (r *shellRepo) run(args ...string) (string, error) {
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
func (r *shellRepo) GitDir() (string, error) {
	out, err := r.run("rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Head returns the current HEAD commit hash. Returns an error on an empty repo.
func (r *shellRepo) Head() (string, error) {
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
func (r *shellRepo) IsAncestor(maybeAncestor, descendant string) bool {
	if maybeAncestor == "" {
		return false
	}
	_, err := r.run("merge-base", "--is-ancestor", maybeAncestor, descendant)
	return err == nil
}

// Vitals gathers the current branch / tracking / working-tree state.
func (r *shellRepo) Vitals() (Vitals, error) {
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

// Branches returns the local branches, most recently committed first, each with
// its upstream tracking state, last-commit date, and last author. Branches with
// no upstream are compared against the repository's default branch instead, so
// feature branches still show a meaningful ahead/behind.
func (r *shellRepo) Branches() ([]Branch, error) {
	// One for-each-ref pass covers name, upstream, ahead/behind vs upstream,
	// tip date, and tip author. Fields are separated by unitSep (never present
	// in refnames or author names), records by newline.
	format := strings.Join([]string{
		"%(HEAD)", "%(refname:short)", "%(upstream:short)",
		"%(upstream:track,nobracket)", "%(committerdate:unix)", "%(authorname)",
	}, unitSep)
	out, err := r.run("for-each-ref", "--sort=-committerdate", "--format="+format, "refs/heads")
	if err != nil {
		return nil, err
	}
	branches := parseBranches(out)

	// Fallback: for branches without an upstream, compare against the default
	// branch so they aren't left with a bare "—".
	base := r.defaultBranch()
	if base != "" {
		for i := range branches {
			b := &branches[i]
			if b.HasCompare || b.Gone || b.Name == base {
				continue
			}
			// left-right count of base...branch is "<behind>\t<ahead>".
			if out, err := r.run("rev-list", "--left-right", "--count", base+"..."+b.Name); err == nil {
				if f := strings.Fields(out); len(f) == 2 {
					b.Behind, _ = strconv.Atoi(f[0])
					b.Ahead, _ = strconv.Atoi(f[1])
					b.HasCompare = true
					b.CompareRef = base
				}
			}
		}
	}
	return branches, nil
}

// defaultBranch resolves the repository's default branch for ahead/behind
// fallback: the remote's HEAD when known, otherwise a local main/master.
func (r *shellRepo) defaultBranch() string {
	if out, err := r.run("symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if s := strings.TrimSpace(out); s != "" {
			return s
		}
	}
	for _, name := range []string{"main", "master"} {
		if _, err := r.run("rev-parse", "--verify", "--quiet", "refs/heads/"+name); err == nil {
			return name
		}
	}
	return ""
}

// parseBranches parses the for-each-ref output produced by Branches. Ahead/behind
// vs the upstream come straight from %(upstream:track); the default-branch
// fallback is layered on by the caller.
func parseBranches(out string) []Branch {
	var branches []Branch
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, unitSep)
		if len(f) < 6 {
			continue
		}
		b := Branch{
			IsHead:     strings.TrimSpace(f[0]) == "*",
			Name:       f[1],
			Upstream:   f[2],
			LastAuthor: f[5],
		}
		switch track := strings.TrimSpace(f[3]); {
		case track == "gone":
			b.Gone = true
		case b.Upstream != "":
			b.Ahead, b.Behind = parseTrack(track)
			b.HasCompare = true
			b.CompareRef = b.Upstream
		}
		if t, err := parseUnix(f[4]); err == nil {
			b.LastCommit = t
		}
		branches = append(branches, b)
	}
	return branches
}

// parseTrack reads git's "%(upstream:track,nobracket)" string, e.g.
// "ahead 1, behind 2", "ahead 3", "behind 4", or "" (in sync).
func parseTrack(s string) (ahead, behind int) {
	for _, part := range strings.Split(s, ",") {
		fields := strings.Fields(part)
		if len(fields) != 2 {
			continue
		}
		n, _ := strconv.Atoi(fields[1])
		switch fields[0] {
		case "ahead":
			ahead = n
		case "behind":
			behind = n
		}
	}
	return ahead, behind
}

// Commits returns non-merge commits in revRange (e.g. "abc123..HEAD"), or the
// entire history when revRange is empty. Results carry numstat-derived file
// lists and insertion/deletion totals.
func (r *shellRepo) Commits(revRange string) ([]Commit, error) {
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
