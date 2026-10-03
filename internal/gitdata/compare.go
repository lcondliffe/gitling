package gitdata

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Comparison is what HEAD changes relative to a base revision. Commits use
// two-dot semantics (on HEAD, not on base); the diff uses three-dot semantics
// (merge base to HEAD), so work that landed on the base since the branch was cut
// never shows up as a change on the branch.
type Comparison struct {
	Base      string // the base as named: --base, or the default branch
	BaseOID   string
	HeadOID   string
	MergeBase string

	Commits     []RecentCommit // newest first, at most the requested limit
	CommitCount int            // every commit in base..HEAD, listed or not

	// Files is every changed path, largest change first. Binary files count
	// toward the file total but not toward Insertions/Deletions.
	Files      []FileChange
	Insertions int
	Deletions  int
}

// FileChange is one path in the merge-base..HEAD diff.
type FileChange struct {
	Path       string
	OldPath    string // pre-rename path; empty unless renamed or copied
	Insertions int
	Deletions  int
	Binary     bool
}

// Compare summarizes HEAD against base, listing at most limit commits. An empty
// base means the default branch. Every revision is resolved to an object ID up
// front, so the log and diff describe exactly the commits reported back.
func (r *Repo) Compare(base string, limit int) (Comparison, error) {
	if base == "" {
		base = r.defaultBranch()
		if base == "" {
			return Comparison{}, errors.New("no default branch to compare against (no origin/HEAD, main or master); pass --base")
		}
	}
	// Refuse option-shaped revisions rather than hand git a flag.
	if strings.HasPrefix(base, "-") {
		return Comparison{}, fmt.Errorf("invalid base %q", base)
	}
	c := Comparison{Base: base}
	var err error
	if c.BaseOID, err = r.resolveCommit(base); err != nil {
		return c, fmt.Errorf("base %q is not a commit in this repository", base)
	}
	if c.HeadOID, err = r.resolveCommit("HEAD"); err != nil {
		return c, errors.New("HEAD has no commits to compare")
	}
	out, err := r.run("merge-base", c.BaseOID, c.HeadOID)
	if err != nil {
		if r.IsShallow() {
			return c, fmt.Errorf("no merge base with %s in this shallow clone; deepen it (git fetch --deepen=<n> or --unshallow)", base)
		}
		return c, fmt.Errorf("HEAD and %s share no history", base)
	}
	c.MergeBase = strings.TrimSpace(out)

	span := c.BaseOID + ".." + c.HeadOID
	if out, err = r.run("rev-list", "--count", span); err != nil {
		return c, err
	}
	c.CommitCount, _ = strconv.Atoi(strings.TrimSpace(out))
	if c.Commits, err = r.recentLog(limit, span); err != nil {
		return c, err
	}

	// -z keeps unusual paths verbatim; external diff drivers and textconv are
	// off so numstat counts the stored bytes. Renames and repo-relative paths
	// are explicit so diff.renames and diff.relative can't change the result.
	out, err = r.run("diff", "--numstat", "-z", "--no-ext-diff", "--no-textconv", "--no-relative", "--find-renames", c.MergeBase, c.HeadOID, "--")
	if err != nil {
		return c, err
	}
	c.Files = parseNumstatZ(out)
	for _, f := range c.Files {
		c.Insertions += f.Insertions
		c.Deletions += f.Deletions
	}
	sort.SliceStable(c.Files, func(i, j int) bool {
		return c.Files[i].Insertions+c.Files[i].Deletions > c.Files[j].Insertions+c.Files[j].Deletions
	})
	return c, nil
}

func (r *Repo) resolveCommit(rev string) (string, error) {
	out, err := r.run("rev-parse", "--verify", "--quiet", rev+"^{commit}")
	return strings.TrimSpace(out), err
}

// parseNumstatZ parses `git diff --numstat -z`. Each entry is
// "<add>\t<del>\t<path>\0", or for a rename/copy "<add>\t<del>\t\0<old>\0<new>\0".
// Binary files report "-" for both counts.
func parseNumstatZ(out string) []FileChange {
	var files []FileChange
	tok := strings.Split(out, "\x00")
	for i := 0; i < len(tok); i++ {
		parts := strings.SplitN(tok[i], "\t", 3)
		if len(parts) < 3 {
			continue
		}
		f := FileChange{Path: parts[2], Binary: parts[0] == "-" && parts[1] == "-"}
		f.Insertions, _ = strconv.Atoi(parts[0])
		f.Deletions, _ = strconv.Atoi(parts[1])
		if f.Path == "" {
			if i+2 >= len(tok) {
				break
			}
			f.OldPath, f.Path = tok[i+1], tok[i+2]
			i += 2
		}
		files = append(files, f)
	}
	return files
}
