package render

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/lcondliffe/gitling/internal/gitdata"
)

// RepoRow is one repository's line in the multi-repo overview. Vitals carries
// only the Status() subset: branch, tracking, and working-tree state. PRs is
// the open pull request count; zero renders blank, so "none" and "couldn't
// ask" look the same, exactly like the dashboard's PR panel.
type RepoRow struct {
	Name   string
	Vitals gitdata.Vitals
	PRs    int
	// MorePRs marks a count that hit the lookup cap: the repo has at least
	// PRs open, not exactly PRs.
	MorePRs bool
}

// ReposModel is everything the multi-repo overview needs to draw itself.
type ReposModel struct {
	Rows  []RepoRow
	Width int
}

// Repos renders the one-line-per-repo overview shown when gitling runs in a
// directory of repositories rather than inside one.
func Repos(w io.Writer, m ReposModel, color bool) {
	p := palette{on: color}

	fmt.Fprintln(w)
	p.header(w, "Repositories", fmt.Sprintf("%d %s", len(m.Rows), plural(len(m.Rows), "repo", "repos")))
	fmt.Fprintln(w)

	nameW, branchW, trackW, dirtyW := 0, 0, 0, 0
	for _, r := range m.Rows {
		nameW = max(nameW, cellLen(r.Name))
		branchW = max(branchW, cellLen(r.Vitals.Branch))
		trackW = max(trackW, cellLen(repoTrack(r.Vitals)))
		dirtyW = max(dirtyW, cellLen(repoDirty(r.Vitals)))
	}
	// One repo on a marathon-named branch shouldn't push every other column
	// off to the right.
	branchW = min(branchW, 40)
	maxNameW := 32
	if m.Width > 0 {
		// dot(2) + name + gap(3) + branch + gap(3) + track + gap(3) + dirty
		// precedes the PR count; whatever's left caps the name column, bounded
		// so it never collapses to unreadable.
		avail := m.Width - 2 - 3 - branchW - 3 - trackW - 3 - dirtyW
		if avail < 8 {
			avail = 8
		}
		if avail < maxNameW {
			maxNameW = avail
		}
	}
	nameW = min(nameW, maxNameW)

	for _, r := range m.Rows {
		v := r.Vitals
		dotColor := cAccent
		if v.DirtyFiles > 0 {
			dotColor = cAmber
		}
		name := truncate(r.Name, nameW)
		namePad := strings.Repeat(" ", nameW-cellLen(name))
		branch := truncate(v.Branch, branchW)
		branchPad := strings.Repeat(" ", branchW-cellLen(branch))
		track := repoTrack(v)
		trackPad := strings.Repeat(" ", trackW-cellLen(track))
		dirty := repoDirty(v)
		dirtyPad := strings.Repeat(" ", dirtyW-cellLen(dirty))

		dirtyCol := p.c(cLabel, dirty)
		if v.DirtyFiles > 0 {
			dirtyCol = p.c(cAmber, dirty)
		}
		line := fmt.Sprintf("%s %s%s   %s%s   %s%s   %s%s",
			p.c(dotColor, "●"),
			p.c(cBright, name), namePad,
			p.c(cLabel, branch), branchPad,
			p.repoTrack(v), trackPad,
			dirtyCol, dirtyPad)
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

// repoDirty is the working-tree cell: "clean" or a dirty-file count.
func repoDirty(v gitdata.Vitals) string {
	if v.DirtyFiles == 0 {
		return "clean"
	}
	return fmt.Sprintf("%d dirty", v.DirtyFiles)
}
