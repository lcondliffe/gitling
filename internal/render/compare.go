package render

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/lcondliffe/gitling/internal/gitdata"
)

// compareFiles caps the changed files listed; the totals line still counts
// every file.
const compareFiles = 15

// CompareModel is the branch change summary view. Width: see Model.
type CompareModel struct {
	Head    string // current branch, or the short hash when detached
	Compare gitdata.Comparison
	Shallow bool // commit counts only cover the history present locally
	Now     time.Time
	Width   int
}

// Compare prints what the current branch changes against a base: the exact
// comparison basis, the commits unique to HEAD, and the merge-base diff stats.
func Compare(w io.Writer, m CompareModel, color bool) {
	p := palette{on: color}
	c := m.Compare

	fmt.Fprintln(w)
	p.header(w, "Compare", m.Head+" vs "+c.Base)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s %s   %s %s   %s %s\n",
		p.c(cLabel, "base"), shortOID(c.BaseOID),
		p.c(cLabel, "head"), shortOID(c.HeadOID),
		p.c(cLabel, "merge base"), shortOID(c.MergeBase))
	fmt.Fprintln(w, "  "+p.c(cLabel, "commits "+c.Base+"..HEAD · diff "+c.Base+"...HEAD"))
	if m.Shallow {
		fmt.Fprintln(w, "  "+p.c(cAmber, "shallow clone: only locally available history is compared"))
	}
	fmt.Fprintln(w)

	p.header(w, "Commits", fmt.Sprintf("%d unique to HEAD", c.CommitCount))
	if len(c.Commits) == 0 {
		fmt.Fprintln(w, "  "+p.c(cLabel, "none: HEAD has nothing that "+c.Base+" lacks"))
	} else {
		p.recent(w, c.Commits, m.Now, m.Width)
		if more := c.CommitCount - len(c.Commits); more > 0 {
			fmt.Fprintln(w, "  "+p.c(cLabel, fmt.Sprintf("… %d more", more)))
		}
	}
	fmt.Fprintln(w)

	p.header(w, "Files", fmt.Sprintf("%d changed  +%s -%s",
		len(c.Files), humanInt(c.Insertions), humanInt(c.Deletions)))
	if len(c.Files) == 0 {
		fmt.Fprintln(w, "  "+p.c(cLabel, "no changes since the merge base"))
		fmt.Fprintln(w)
		return
	}
	shown := c.Files[:min(len(c.Files), compareFiles)]
	addW, delW := 0, 0
	for _, f := range shown {
		addW = max(addW, len(strconv.Itoa(f.Insertions))+1)
		delW = max(delW, len(strconv.Itoa(f.Deletions))+1)
	}
	statW := max(addW+1+delW, len("binary"))
	// "  " + stat + "   " precedes the path.
	pathW := pathBudget(m.Width, 2+statW+3)
	for _, f := range shown {
		stat := p.c(cLabel, fmt.Sprintf("%-*s", statW, "binary"))
		if !f.Binary {
			stat = p.c(cAccent, fmt.Sprintf("%*s", addW, "+"+strconv.Itoa(f.Insertions))) + " " +
				p.c(cRed, fmt.Sprintf("%*s", delW, "-"+strconv.Itoa(f.Deletions))) +
				strings.Repeat(" ", statW-addW-1-delW)
		}
		path := displayPath(f.Path)
		if f.OldPath != "" {
			path = displayPath(f.OldPath) + " → " + path
		}
		fmt.Fprintf(w, "  %s   %s\n", stat, elidePath(path, pathW))
	}
	if more := len(c.Files) - len(shown); more > 0 {
		fmt.Fprintln(w, "  "+p.c(cLabel, fmt.Sprintf("… %d more", more)))
	}
	fmt.Fprintln(w)
}

// displayPath quotes a path holding control characters (a newline, tab or
// escape in a filename), as git does, so it stays on one inert line.
func displayPath(path string) string {
	if strings.ContainsFunc(path, unicode.IsControl) {
		return strconv.Quote(path)
	}
	return path
}

func shortOID(oid string) string {
	return oid[:min(len(oid), 7)]
}
