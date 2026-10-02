package render

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/lcondliffe/gitling/internal/gitdata"
)

// RepoRow is one checkout's line in the multi-repo overview. Vitals carries
// only the Status() subset: branch, tracking, working-tree state, operation,
// and last fetch. PRs is the open pull request count; zero renders blank, so
// "none" and "couldn't ask" look the same, exactly like the dashboard's PR
// panel.
type RepoRow struct {
	Name   string
	Vitals gitdata.Vitals
	PRs    int
	// MorePRs marks a count that hit the lookup cap: the repo has at least
	// PRs open, not exactly PRs.
	MorePRs bool
	// FetchFailed marks a --fetch that didn't complete.
	FetchFailed bool
	// Err is why the checkout couldn't be probed; shown instead of its state.
	Err string
}

// ReposModel is everything the multi-repo overview needs to draw itself.
type ReposModel struct {
	Rows []RepoRow
	// Hidden counts rows --only attention left out.
	Hidden int
	Now    time.Time
	Width  int
}

// staleWorkspaceFetch is when a fetch counts against a checkout. Longer than
// staleFetch: across a workspace, a day-old fetch is the norm.
const staleWorkspaceFetch = 7 * 24 * time.Hour

// AttentionClean is the Attention rank of a row with nothing to act on.
const AttentionClean = 5

// Attention ranks a row for --sort/--only attention, most urgent (0) first.
// The order is documented in the README.
func Attention(r RepoRow, now time.Time) int {
	v := r.Vitals
	switch {
	case v.Conflicts > 0:
		return 0
	case v.Operation.InProgress():
		return 1
	case v.DirtyFiles > 0:
		return 2
	case v.Ahead > 0:
		return 3
	case r.Err != "" || r.FetchFailed || fetchStale(v, now):
		return 4
	}
	return AttentionClean
}

func fetchStale(v gitdata.Vitals, now time.Time) bool {
	return v.HasUpstream && !v.LastFetch.IsZero() && now.Sub(v.LastFetch) >= staleWorkspaceFetch
}

// Repos renders the one-line-per-repo overview shown when gitling runs in a
// directory of repositories rather than inside one.
func Repos(w io.Writer, m ReposModel, color bool) {
	p := palette{on: color}

	fmt.Fprintln(w)
	sub := fmt.Sprintf("%d %s", len(m.Rows), plural(len(m.Rows), "repo", "repos"))
	if m.Hidden > 0 {
		sub += fmt.Sprintf(" · %d clean hidden", m.Hidden)
	}
	p.header(w, "Repositories", sub)
	fmt.Fprintln(w)

	// Columns after the name, in order. A column no row uses takes no space.
	type cell struct{ plain, colored string }
	cols := make([][]cell, len(m.Rows))
	const nCols = 5 // branch, track, dirty, operation, fetch
	widths := make([]int, nCols)
	nameW := 0
	for i, r := range m.Rows {
		nameW = max(nameW, cellLen(r.Name))
		if r.Err != "" {
			continue
		}
		v := r.Vitals
		op := ""
		if v.Operation.InProgress() {
			op = operationLabel(v.Operation)
		}
		fetchColor, fetch := cLabel, ""
		switch {
		case r.FetchFailed:
			fetchColor, fetch = cRed, "fetch failed"
		case !v.LastFetch.IsZero():
			fetch = "fetched " + humanAgo(v.LastFetch, m.Now)
			if fetchStale(v, m.Now) {
				fetchColor = cAmber
			}
		}
		dirtyColor := cLabel
		switch {
		case v.Conflicts > 0:
			dirtyColor = cRed
		case v.DirtyFiles > 0:
			dirtyColor = cAmber
		}
		branch := truncate(v.Branch, 40) // one marathon-named branch shouldn't push every other column right
		cols[i] = []cell{
			{branch, p.c(cLabel, branch)},
			{repoTrack(v), p.repoTrack(v)},
			{repoDirty(v), p.c(dirtyColor, repoDirty(v))},
			{op, p.c(cRed, op)},
			{fetch, p.c(fetchColor, fetch)},
		}
		for j, c := range cols[i] {
			widths[j] = max(widths[j], cellLen(c.plain))
		}
	}
	maxNameW := 32
	if m.Width > 0 {
		// Whatever the dot and the other columns leave caps the name column,
		// bounded so it never collapses to unreadable.
		avail := m.Width - 2
		for _, cw := range widths {
			if cw > 0 {
				avail -= 3 + cw
			}
		}
		maxNameW = min(maxNameW, max(avail, 8))
	}
	nameW = min(nameW, maxNameW)

	for i, r := range m.Rows {
		v := r.Vitals
		dotColor := cAccent
		switch {
		case r.Err != "" || v.Operation.InProgress() || v.Conflicts > 0:
			dotColor = cRed
		case v.DirtyFiles > 0:
			dotColor = cAmber
		}
		name := truncateName(r.Name, nameW)
		line := p.c(dotColor, "●") + " " + p.c(cBright, name) + strings.Repeat(" ", nameW-cellLen(name))
		if r.Err != "" {
			msg := r.Err
			if m.Width > 0 {
				msg = truncate(msg, max(m.Width-2-2-nameW-3, 8))
			}
			line += "   " + p.c(cRed, msg)
		}
		for j, c := range cols[i] {
			if widths[j] > 0 {
				line += "   " + c.colored + strings.Repeat(" ", widths[j]-cellLen(c.plain))
			}
		}
		if r.PRs > 0 {
			count := strconv.Itoa(r.PRs)
			if r.MorePRs {
				count += "+"
			}
			line += fmt.Sprintf("   %s", p.c(cLabel, count+" "+plural(r.PRs, "PR", "PRs")))
		}
		fmt.Fprintln(w, "  "+strings.TrimRight(line, " "))
	}
	fmt.Fprintln(w)
}

// truncateName is truncate, except a path loses its start, not its end.
func truncateName(name string, max int) string {
	if !strings.ContainsAny(name, `/\`) || cellLen(name) <= max || max <= 1 {
		return truncate(name, max)
	}
	r := []rune(name)
	for cellLen(string(r)) > max-1 {
		r = r[1:]
	}
	return "…" + string(r)
}

// repoTrack is the plain (uncolored) ahead/behind cell, used for width.
func repoTrack(v gitdata.Vitals) string {
	if !v.HasUpstream {
		return "—"
	}
	return fmt.Sprintf("↑%d ↓%d", v.Ahead, v.Behind)
}

// repoTrack (method) is the colored version of the same cell; it has the same
// visible width as the plain form so column padding still lines up.
func (p palette) repoTrack(v gitdata.Vitals) string {
	if !v.HasUpstream {
		return p.c(cLabel, "—")
	}
	ahead := p.c(cLabel, fmt.Sprintf("↑%d", v.Ahead))
	if v.Ahead > 0 {
		ahead = p.c(cAccent, fmt.Sprintf("↑%d", v.Ahead))
	}
	behind := p.c(cLabel, fmt.Sprintf("↓%d", v.Behind))
	if v.Behind > 0 {
		behind = p.c(cAmber, fmt.Sprintf("↓%d", v.Behind))
	}
	return ahead + " " + behind
}

// repoDirty is the working-tree cell: conflicts, else a dirty count, else "clean".
func repoDirty(v gitdata.Vitals) string {
	if v.Conflicts > 0 {
		return fmt.Sprintf("%d %s", v.Conflicts, plural(v.Conflicts, "conflict", "conflicts"))
	}
	if v.DirtyFiles == 0 {
		return "clean"
	}
	return fmt.Sprintf("%d dirty", v.DirtyFiles)
}
