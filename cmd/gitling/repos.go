package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lcondliffe/gitling/internal/forge"
	"github.com/lcondliffe/gitling/internal/gitdata"
	"github.com/lcondliffe/gitling/internal/render"
)

// overviewPRLimit caps the open-PR count per repo; the column answers "is
// anything waiting", not "exactly how much".
const overviewPRLimit = 50

// repoProbes bounds how many repositories are probed at once. Each probe is a
// handful of short-lived git processes (plus a forge CLI call and, with
// --fetch, a network fetch), so this is about not forking hundreds at once in
// a huge directory, not about raw speed.
const repoProbes = 16

// childRepos lists the immediate subdirectories of dir that are git
// repositories, in directory order (alphabetical). Only one level down: the
// common ~/repo/* layout, not a tree walk.
func childRepos(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// A .git of any kind (dir, or file in worktrees/submodules) marks a repo.
		if _, err := os.Stat(filepath.Join(dir, e.Name(), ".git")); err == nil {
			names = append(names, e.Name())
		}
	}
	return names
}

// checkout is one working tree to probe; common is its repo's common git dir.
type checkout struct {
	path   string
	common string
	err    error
}

// runRepos renders the multi-repo overview: one line of Status (plus an open
// PR count) per checkout. Read-only apart from --fetch; per-checkout failures
// stay in the list and are warned about on stderr.
func runRepos(stdout io.Writer, o options, names []string) error {
	if o.json {
		return errors.New("--json is not available for the multi-repo overview")
	}

	// Open each child repo and, with --worktrees, list its linked worktrees.
	found := make([][]checkout, len(names))
	bounded(len(names), func(i int) {
		repo, err := gitdata.Open(names[i])
		if err != nil {
			found[i] = []checkout{{path: names[i], err: err}}
			return
		}
		common, err := repo.CommonDir()
		if err != nil {
			found[i] = []checkout{{path: names[i], err: err}}
			return
		}
		found[i] = []checkout{{path: names[i], common: common}}
		if o.worktrees {
			paths, err := repo.Worktrees()
			if err != nil {
				warn(names[i], err)
			}
			for _, wt := range paths {
				found[i] = append(found[i], checkout{path: wt, common: common})
			}
		}
	})

	// Dedupe by real path, then group by repo so fetch and PR lookup run once.
	seen := map[string]bool{}
	var order []string
	groups := map[string][]checkout{}
	for _, cs := range found {
		for _, c := range cs {
			key := realPath(c.path)
			if seen[key] {
				continue
			}
			seen[key] = true
			g := c.common
			if g == "" {
				g = "\x00" + key // unopenable: a group of its own
			}
			if _, ok := groups[g]; !ok {
				order = append(order, g)
			}
			groups[g] = append(groups[g], c)
		}
	}

	rows := make([][]render.RepoRow, len(order))
	bounded(len(order), func(i int) { rows[i] = probeGroup(o, groups[order[i]]) })

	now := time.Now()
	m := render.ReposModel{Width: o.width, Now: now}
	for _, rs := range rows {
		for _, r := range rs {
			if o.only == "attention" && render.Attention(r, now) == render.AttentionClean {
				m.Hidden++
				continue
			}
			m.Rows = append(m.Rows, r)
		}
	}
	if len(m.Rows)+m.Hidden == 0 {
		return fmt.Errorf("not a git repository (and no git repositories found in the current directory)")
	}
	if o.sort == "attention" {
		// Stable, so rows of equal rank keep discovery (alphabetical) order.
		slices.SortStableFunc(m.Rows, func(a, b render.RepoRow) int {
			return render.Attention(a, now) - render.Attention(b, now)
		})
	}
	render.Repos(stdout, m, o.color)
	return nil
}

// probeGroup probes one repo's checkouts: one fetch and PR lookup, then each
// checkout's own state.
func probeGroup(o options, cs []checkout) []render.RepoRow {
	rows := make([]render.RepoRow, len(cs))
	fetched, fetchErr := false, error(nil)
	prs, prsDone := 0, false
	for i, c := range cs {
		rows[i].Name = displayName(c.path)
		if c.err != nil {
			rows[i].Err = shortErr(c.err)
			warn(c.path, c.err)
			continue
		}
		repo, err := gitdata.Open(c.path)
		if err != nil {
			rows[i].Err = shortErr(err)
			warn(c.path, err)
			continue
		}
		if o.fetch && !fetched {
			fetched = true
			if fetchErr = repo.Fetch(true); fetchErr != nil {
				warn(c.path, fetchErr)
			}
		}
		rows[i].FetchFailed = fetchErr != nil
		rows[i].Vitals = repo.Status()
		if o.prs {
			if !prsDone {
				prsDone = true
				prs = len(forge.List(c.path, repo.RemoteURL(), overviewPRLimit))
			}
			rows[i].PRs, rows[i].MorePRs = prs, prs == overviewPRLimit
		}
	}
	return rows
}

// bounded runs f(0..n-1) concurrently, at most repoProbes at a time.
func bounded(n int, f func(i int)) {
	sem := make(chan struct{}, repoProbes)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			f(i)
		})
	}
	wg.Wait()
}

// realPath canonicalizes a path for dedupe, falling back to the absolute path.
func realPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

// displayName is the path relative to the cwd when inside it, else ~-relative.
func displayName(path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(realPath(cwd), realPath(path)); err == nil && filepath.IsLocal(rel) {
			return rel
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, path); err == nil && filepath.IsLocal(rel) {
			return filepath.Join("~", rel)
		}
	}
	return path
}

// shortErr trims a probe error to git's "fatal:" message when there is one.
func shortErr(err error) string {
	msg := err.Error()
	if _, after, ok := strings.Cut(msg, "fatal: "); ok {
		return after
	}
	if errors.Is(err, fs.ErrNotExist) {
		return "missing"
	}
	return msg
}

func warn(path string, err error) {
	fmt.Fprintf(os.Stderr, "gitling: warning: %s: %v\n", path, err)
}
