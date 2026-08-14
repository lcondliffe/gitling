package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

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

// runRepos renders the multi-repo overview: one line of Status (plus an open
// PR count) per child repository. Read-only apart from the opt-in --fetch;
// anything that fails per repo degrades to what the local refs already know.
func runRepos(stdout io.Writer, o options, names []string) error {
	if o.json {
		return errors.New("--json is not available for the multi-repo overview")
	}

	rows := make([]*render.RepoRow, len(names))
	sem := make(chan struct{}, repoProbes)
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			repo, err := gitdata.Open(name)
			if err != nil {
				return // looked like a repo but isn't; skip the row
			}
			if o.fetch {
				// Failure (offline, credentials) degrades to local refs.
				_ = repo.Fetch(true)
			}
			row := &render.RepoRow{Name: name, Vitals: repo.Status()}
			if o.prs {
				row.PRs = len(forge.List(name, repo.RemoteURL(), overviewPRLimit))
			}
			rows[i] = row
		}()
	}
	wg.Wait()

	m := render.ReposModel{Width: o.width}
	for _, r := range rows {
		if r != nil {
			m.Rows = append(m.Rows, *r)
		}
	}
	if len(m.Rows) == 0 {
		return fmt.Errorf("not a git repository (and no git repositories found in the current directory)")
	}
	render.Repos(stdout, m, o.color)
	return nil
}
