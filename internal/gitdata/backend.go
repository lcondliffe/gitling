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
	Commits(revRange string) ([]Commit, error)
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

// Commits returns non-merge commits in revRange, or the entire history when
// revRange is empty.
func (r *Repo) Commits(revRange string) ([]Commit, error) { return r.backend.Commits(revRange) }
