//go:build gogit

package gitdata

import (
	"fmt"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/storage/filesystem"
)

// gogitRepo is the opt-in pure-Go Backend implementation, built with
// `-tags gogit`. It talks to the on-disk repository directly via go-git
// instead of shelling out to `git`.
//
// Known divergences from the shell-out backend (shellRepo in gitdata.go),
// documented here rather than silently producing wrong numbers:
//
//   - Author name/email are NOT mailmap-resolved (go-git reads the raw commit
//     header), whereas shell-out uses `%aN`/`%aE` which apply .mailmap. Repos
//     using .mailmap to collapse split identities will see more distinct
//     authors under this backend.
//   - StashCount is always 0: go-git has no porcelain equivalent of
//     `git stash list` (it does not track refs/stash as a reflog-backed
//     stack), so this is a known gap rather than a best-effort guess.
//   - Ahead/behind counts are computed with a simple merge-base + linear
//     history walk (see aheadBehind below), which is O(history) rather than
//     git's optimized graph walk. For very large histories this is slower
//     but produces the same counts for the common case of no criss-cross
//     merges between the two tips.
//   - Renames: go-git's Commit.Stats() reports the file's current (post-
//     rename) path per changed entry, matching shell-out's numstat handling
//     (cleanPath resolves to the new name), but it does not separately
//     surface the old name.
type gogitRepo struct {
	repo *git.Repository
}

// openGogit opens dir as a git repository via go-git, walking up to find the
// enclosing work tree the same way `git rev-parse` does.
func openGogit(dir string) (*gogitRepo, error) {
	repo, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{
		DetectDotGit: true,
		// Required for linked worktrees (`git worktree add`), where objects
		// and refs/heads live in the main .git dir but HEAD is per-worktree.
		EnableDotGitCommonDir: true,
	})
	if err != nil {
		return nil, fmt.Errorf("not a git repository (go-git): %w", err)
	}
	return &gogitRepo{repo: repo}, nil
}

// GitDir returns the absolute path to the repository's .git directory. This
// only works for the standard filesystem-backed storage, which is what
// PlainOpenWithOptions always produces.
func (g *gogitRepo) GitDir() (string, error) {
	fs, ok := g.repo.Storer.(*filesystem.Storage)
	if !ok {
		return "", fmt.Errorf("gogit: repository storage is not filesystem-backed")
	}
	return fs.Filesystem().Root(), nil
}

// Head returns the current HEAD commit hash.
func (g *gogitRepo) Head() (string, error) {
	ref, err := g.repo.Head()
	if err != nil {
		return "", err
	}
	return ref.Hash().String(), nil
}

// IsAncestor reports whether maybeAncestor is an ancestor of descendant.
func (g *gogitRepo) IsAncestor(maybeAncestor, descendant string) bool {
	if maybeAncestor == "" {
		return false
	}
	aHash, err := g.repo.ResolveRevision(plumbing.Revision(maybeAncestor))
	if err != nil {
		return false
	}
	dHash, err := g.repo.ResolveRevision(plumbing.Revision(descendant))
	if err != nil {
		return false
	}
	aCommit, err := g.repo.CommitObject(*aHash)
	if err != nil {
		return false
	}
	dCommit, err := g.repo.CommitObject(*dHash)
	if err != nil {
		return false
	}
	ok, err := aCommit.IsAncestor(dCommit)
	if err != nil {
		return false
	}
	return ok
}

// Vitals gathers the current branch / tracking / working-tree state.
func (g *gogitRepo) Vitals() (Vitals, error) {
	var v Vitals

	head, err := g.repo.Head()
	if err != nil {
		// No commits yet (or detached with no ref at all).
		v.Branch = "(no commits)"
		v.Detached = true
		if count, cErr := g.branchCount(); cErr == nil {
			v.BranchCount = count
		}
		return v, nil
	}

	if head.Name().IsBranch() {
		v.Branch = head.Name().Short()
	} else {
		v.Detached = true
		v.Branch = head.Hash().String()
		if len(v.Branch) > 7 {
			v.Branch = v.Branch[:7]
		}
	}

	if !v.Detached {
		if branchCfg, bErr := g.repo.Branch(v.Branch); bErr == nil && branchCfg.Remote != "" && branchCfg.Merge != "" {
			remoteRefName := plumbing.NewRemoteReferenceName(branchCfg.Remote, branchCfg.Merge.Short())
			if remoteRef, rErr := g.repo.Reference(remoteRefName, true); rErr == nil {
				v.HasUpstream = true
				if ahead, behind, aErr := aheadBehind(g.repo, head.Hash(), remoteRef.Hash()); aErr == nil {
					v.Ahead, v.Behind = ahead, behind
				}
			}
		}
	}

	if wt, wErr := g.repo.Worktree(); wErr == nil {
		if st, sErr := wt.Status(); sErr == nil {
			v.DirtyFiles = len(st)
		}
	}

	// go-git has no porcelain `git stash list` equivalent; see the type-level
	// comment on gogitRepo for why this is a documented 0 rather than a
	// best-effort guess.
	v.StashCount = 0

	if count, cErr := g.branchCount(); cErr == nil {
		v.BranchCount = count
	}

	return v, nil
}

func (g *gogitRepo) branchCount() (int, error) {
	refs, err := g.repo.Branches()
	if err != nil {
		return 0, err
	}
	count := 0
	err = refs.ForEach(func(*plumbing.Reference) error {
		count++
		return nil
	})
	return count, err
}

// Branches returns the local branches, most recently committed first, each
// with its upstream tracking state, last-commit date, and last author.
// Branches with no upstream are compared against the repository's default
// branch instead, mirroring the shell-out backend's fallback.
func (g *gogitRepo) Branches() ([]Branch, error) {
	refIter, err := g.repo.Branches()
	if err != nil {
		return nil, err
	}

	var headName plumbing.ReferenceName
	if head, hErr := g.repo.Head(); hErr == nil {
		headName = head.Name()
	}

	var branches []Branch
	err = refIter.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name().Short()
		b := Branch{
			Name:   name,
			IsHead: ref.Name() == headName,
		}
		if commit, cErr := g.repo.CommitObject(ref.Hash()); cErr == nil {
			b.LastCommit = commit.Committer.When
			b.LastAuthor = commit.Author.Name
		}
		if branchCfg, bErr := g.repo.Branch(name); bErr == nil && branchCfg.Remote != "" && branchCfg.Merge != "" {
			b.Upstream = branchCfg.Remote + "/" + branchCfg.Merge.Short()
			remoteRefName := plumbing.NewRemoteReferenceName(branchCfg.Remote, branchCfg.Merge.Short())
			if remoteRef, rErr := g.repo.Reference(remoteRefName, true); rErr == nil {
				if ahead, behind, aErr := aheadBehind(g.repo, ref.Hash(), remoteRef.Hash()); aErr == nil {
					b.Ahead, b.Behind = ahead, behind
					b.HasCompare = true
					b.CompareRef = b.Upstream
				}
			} else {
				// Upstream configured but the remote-tracking ref is gone.
				b.Gone = true
			}
		}
		branches = append(branches, b)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.SliceStable(branches, func(i, j int) bool {
		return branches[i].LastCommit.After(branches[j].LastCommit)
	})

	if base := g.defaultBranch(branches); base != "" {
		if baseHash, bErr := g.repo.ResolveRevision(plumbing.Revision(base)); bErr == nil {
			for i := range branches {
				bb := &branches[i]
				if bb.HasCompare || bb.Gone || bb.Name == base {
					continue
				}
				tipHash, tErr := g.repo.ResolveRevision(plumbing.Revision(bb.Name))
				if tErr != nil {
					continue
				}
				if ahead, behind, aErr := aheadBehind(g.repo, *tipHash, *baseHash); aErr == nil {
					bb.Ahead, bb.Behind = ahead, behind
					bb.HasCompare = true
					bb.CompareRef = base
				}
			}
		}
	}

	return branches, nil
}

// defaultBranch resolves the repository's default branch for ahead/behind
// fallback: the remote's HEAD when known, otherwise a local main/master.
func (g *gogitRepo) defaultBranch(branches []Branch) string {
	if ref, err := g.repo.Reference(plumbing.NewRemoteHEADReferenceName("origin"), true); err == nil {
		short := ref.Name().Short()
		if idx := strings.Index(short, "/"); idx >= 0 {
			return short[idx+1:]
		}
		return short
	}
	for _, name := range []string{"main", "master"} {
		for _, b := range branches {
			if b.Name == name {
				return name
			}
		}
	}
	return ""
}

// Commits returns non-merge commits in revRange ("" = full history, or
// "hash..HEAD" for an incremental walk), with numstat-equivalent per-commit
// insertion/deletion counts and touched file paths, computed by diffing each
// commit against its first parent (go-git's Commit.Stats, which mirrors
// `git diff --stat` semantics).
func (g *gogitRepo) Commits(revRange string) ([]Commit, error) {
	var from plumbing.Hash
	var stopAt *plumbing.Hash // exclusive lower bound for "since..until" ranges

	if revRange == "" {
		head, err := g.repo.Head()
		if err != nil {
			return nil, err
		}
		from = head.Hash()
	} else {
		parts := strings.SplitN(revRange, "..", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("gogit: unsupported rev range %q", revRange)
		}
		sinceHash, err := g.repo.ResolveRevision(plumbing.Revision(parts[0]))
		if err != nil {
			return nil, err
		}
		untilHash, err := g.repo.ResolveRevision(plumbing.Revision(parts[1]))
		if err != nil {
			return nil, err
		}
		from = *untilHash
		stopAt = sinceHash
	}

	logIter, err := g.repo.Log(&git.LogOptions{From: from, Order: git.LogOrderCommitterTime})
	if err != nil {
		return nil, err
	}
	defer logIter.Close()

	var commits []Commit
	err = logIter.ForEach(func(c *object.Commit) error {
		if stopAt != nil && c.Hash == *stopAt {
			return storer.ErrStop
		}
		if c.NumParents() > 1 {
			return nil // skip merges, matches shell-out's --no-merges
		}
		commit := Commit{
			Hash:        c.Hash.String(),
			AuthorName:  c.Author.Name,
			AuthorEmail: c.Author.Email,
			AuthorTime:  c.Author.When,
			CommitTime:  c.Committer.When,
		}
		if stats, sErr := c.Stats(); sErr == nil {
			for _, s := range stats {
				commit.Insertions += s.Addition
				commit.Deletions += s.Deletion
				commit.Files = append(commit.Files, s.Name)
			}
		}
		commits = append(commits, commit)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return commits, nil
}

// aheadBehind counts commits reachable from a but not b ("ahead") and from b
// but not a ("behind"), via their merge-base. This is a straightforward
// linear-history walk rather than git's optimized graph algorithm: correct
// for the common case (no criss-cross merges between the two tips), but
// O(history) rather than git's near-constant-time count.
func aheadBehind(repo *git.Repository, a, b plumbing.Hash) (ahead, behind int, err error) {
	if a == b {
		return 0, 0, nil
	}
	ac, err := repo.CommitObject(a)
	if err != nil {
		return 0, 0, err
	}
	bc, err := repo.CommitObject(b)
	if err != nil {
		return 0, 0, err
	}
	bases, err := ac.MergeBase(bc)
	if err != nil {
		return 0, 0, err
	}
	stop := map[plumbing.Hash]bool{}
	for _, base := range bases {
		stop[base.Hash] = true
	}
	if ahead, err = countCommitsUntil(repo, a, stop); err != nil {
		return 0, 0, err
	}
	if behind, err = countCommitsUntil(repo, b, stop); err != nil {
		return 0, 0, err
	}
	return ahead, behind, nil
}

// countCommitsUntil counts commits reachable from from, stopping (without
// counting) as soon as it reaches a commit in stop.
func countCommitsUntil(repo *git.Repository, from plumbing.Hash, stop map[plumbing.Hash]bool) (int, error) {
	iter, err := repo.Log(&git.LogOptions{From: from, Order: git.LogOrderCommitterTime})
	if err != nil {
		return 0, err
	}
	defer iter.Close()
	count := 0
	err = iter.ForEach(func(c *object.Commit) error {
		if stop[c.Hash] {
			return storer.ErrStop
		}
		count++
		return nil
	})
	return count, err
}
