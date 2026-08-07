package gitdata

// Backend is the git interaction surface gitling needs. The default build
// only ever compiles the shell-out implementation (shellRepo in gitdata.go);
// an optional pure-Go go-git implementation (gogitRepo in gogit.go) is
// available behind the `gogit` build tag. See newBackend in
// backend_default.go / backend_gogit.go for how the concrete backend is
// chosen.
type Backend interface {
	GitDir() (string, error)
	Head() (string, error)
	IsAncestor(maybeAncestor, descendant string) bool
	Vitals() (Vitals, error)
	Branches() ([]Branch, error)
	DefaultBranch() string
	Commits(revRange string) ([]Commit, error)
	RecentCommits(limit int) ([]RecentCommit, error)

	// Mutating operations, used only by the tidy command. Every other caller is
	// read-only, which is what makes gitling safe to run anywhere.
	Fetch(prune bool) error
	DeleteBranch(name string, force bool) error
}

// Repo is a handle to a git repository, backed by whichever Backend was
// selected at build (and optionally run) time. Its public API is stable
// across backends so callers never need to know which one is in use.
type Repo struct {
	backend Backend
}

// Open verifies dir is inside a git work tree and returns a Repo backed by
// the selected Backend implementation.
func Open(dir string) (*Repo, error) {
	b, err := newBackend(dir)
	if err != nil {
		return nil, err
	}
	return &Repo{backend: b}, nil
}

// GitDir returns the absolute path to the repository's git directory.
func (r *Repo) GitDir() (string, error) { return r.backend.GitDir() }

// Head returns the current HEAD commit hash.
func (r *Repo) Head() (string, error) { return r.backend.Head() }

// IsAncestor reports whether maybeAncestor is an ancestor of descendant.
func (r *Repo) IsAncestor(maybeAncestor, descendant string) bool {
	return r.backend.IsAncestor(maybeAncestor, descendant)
}

// Vitals gathers the current branch / tracking / working-tree state.
func (r *Repo) Vitals() (Vitals, error) { return r.backend.Vitals() }

// Branches returns the local branches for the branches drill-down.
func (r *Repo) Branches() ([]Branch, error) { return r.backend.Branches() }

// DefaultBranch returns the repository's default branch ref, or "" when it
// cannot be resolved.
func (r *Repo) DefaultBranch() string { return r.backend.DefaultBranch() }

// Fetch updates remote-tracking refs, pruning deleted ones when prune is set.
func (r *Repo) Fetch(prune bool) error { return r.backend.Fetch(prune) }

// DeleteBranch deletes a local branch, using git's merge check unless force.
func (r *Repo) DeleteBranch(name string, force bool) error {
	return r.backend.DeleteBranch(name, force)
}

// Commits returns non-merge commits in revRange, or the entire history when
// revRange is empty.
func (r *Repo) Commits(revRange string) ([]Commit, error) { return r.backend.Commits(revRange) }

// RecentCommits returns up to limit commits from the tip of HEAD, newest first,
// merges included.
func (r *Repo) RecentCommits(limit int) ([]RecentCommit, error) {
	return r.backend.RecentCommits(limit)
}
