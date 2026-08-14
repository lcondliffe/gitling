package render

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lcondliffe/gitling/internal/tidy"
)

// TidyModel is the branch-cleanup plan view. Applied marks the run as having
// actually deleted the branches rather than only proposing to; Width: see Model.
type TidyModel struct {
	Plan    tidy.Plan
	Now     time.Time
	Width   int
	Applied bool
	// Confirming marks the plan as about to be applied, pending the user's
	// answer. It suppresses the "dry run" footer, which would otherwise
	// contradict the prompt that follows it.
	Confirming bool
	// Failed maps a branch name to the error that stopped it being deleted, for
	// the applied case. A branch in here was attempted and left alone.
	Failed map[string]error
}

// reasonHeadings label each group, and say plainly what evidence puts a branch
// in it — the difference between "provably merged" and "probably merged" is the
// whole safety story of this command.
var reasonHeadings = map[tidy.Reason]string{
	tidy.ReasonMerged: "merged into %s",
	tidy.ReasonGone:   "upstream gone (squash-merged)",
	tidy.ReasonStale:  "stale, never merged",
}

// Tidy prints the cleanup plan: the candidate branches grouped by why they were
// selected, what was protected, and either how to apply the plan or what
// applying it did.
func Tidy(w io.Writer, m TidyModel, color bool) {
	p := palette{on: color}

	fmt.Fprintln(w)
	p.header(w, "Tidy", tidyHeaderSuffix(m))
	fmt.Fprintln(w)

	if m.Plan.Empty() {
		p.tidyLine(w, m.Width, cLabel, "nothing to clean up")
		p.tidyProtected(w, m)
		fmt.Fprintln(w)
		return
	}

	nameW, ageW := 0, 0
	for _, c := range m.Plan.Candidates {
		if n := cellLen(c.Branch.Name); n > nameW {
			nameW = n
		}
		if n := cellLen(humanAgo(c.Branch.LastCommit, m.Now)); n > ageW {
			ageW = n
		}
	}
	if max := tidyMaxNameWidth(m.Width); nameW > max {
		nameW = max
	}

	var lastReason tidy.Reason
	for _, c := range m.Plan.Candidates {
		if c.Reason != lastReason {
			if lastReason != "" {
				fmt.Fprintln(w)
			}
			lastReason = c.Reason
			flag := "-d"
			if c.Force {
				flag = "-D"
			}
			heading := tidyHeading(c.Reason, m.Plan.Base)
			// The flag is the honest signal of how much git itself vouches for
			// the deletion, so it keeps its own colour and is never the part
			// that gets cut.
			if width := m.Width - 5 - cellLen(flag); width > 0 && cellLen(heading) > width {
				heading = truncate(heading, width)
			}
			fmt.Fprintln(w, "  "+p.c(cLabel, heading)+"   "+p.tidyFlag(c.Force))
		}
		fmt.Fprintln(w, "    "+strings.TrimRight(p.tidyRow(c, m, nameW, ageW), " "))
	}

	fmt.Fprintln(w)
	p.tidySummary(w, m)
	p.tidyProtected(w, m)
	fmt.Fprintln(w)
}

// TidyResult reports what applying the plan did, without repeating the plan. It
// is for the confirmation path, where the list is already on screen directly
// above the prompt and printing it a second time says nothing new.
func TidyResult(w io.Writer, m TidyModel, color bool) {
	p := palette{on: color}
	deleted := len(m.Plan.Candidates) - len(m.Failed)

	fmt.Fprintln(w)
	p.tidyLine(w, m.Width, cAccent, fmt.Sprintf("%d %s deleted", deleted, plural(deleted, "branch", "branches")))
	// Name the branches git refused: with no table reprinted, this is the only
	// place the failure is reported.
	for _, c := range m.Plan.Candidates {
		if err, failed := m.Failed[c.Branch.Name]; failed {
			p.tidyLine(w, m.Width, cRed, fmt.Sprintf("kept %s: %s", c.Branch.Name, firstLine(err.Error())))
		}
	}
	if deleted > 0 {
		p.tidyLine(w, m.Width, cLabel, "restore any of these with: git branch <name> <hash>")
	}
	fmt.Fprintln(w)
}

func tidyHeaderSuffix(m TidyModel) string {
	n := len(m.Plan.Candidates)
	if m.Applied {
		return fmt.Sprintf("%d %s deleted", n-len(m.Failed), plural(n-len(m.Failed), "branch", "branches"))
	}
	return fmt.Sprintf("%d of %d %s", n, m.Plan.Total, plural(m.Plan.Total, "branch", "branches"))
}

func tidyHeading(r tidy.Reason, base string) string {
	heading := reasonHeadings[r]
	if strings.Contains(heading, "%s") {
		if base == "" {
			base = "the default branch"
		}
		return fmt.Sprintf(heading, base)
	}
	return heading
}

// tidyFlag names the git flag a group needs, which is the honest signal of how
// much git itself is willing to vouch for the deletion.
func (p palette) tidyFlag(force bool) string {
	if force {
		return p.c(cAmber, "-D")
	}
	return p.c(cLabel, "-d")
}

// tidyMaxNameWidth caps the branch column so long names can't push the age and
// hash off a narrow terminal.
func tidyMaxNameWidth(width int) int {
	const cap = 40
	if width <= 0 {
		return cap
	}
	// indent(4) + name + gap(3) + age(~13) + gap(3) + short hash(7).
	avail := width - 4 - 3 - 13 - 3 - 7
	if avail < 8 {
		avail = 8
	}
	if avail < cap {
		return avail
	}
	return cap
}

func (p palette) tidyRow(c tidy.Candidate, m TidyModel, nameW, ageW int) string {
	name := truncate(c.Branch.Name, nameW)
	pad := strings.Repeat(" ", nameW-cellLen(name))
	age := humanAgo(c.Branch.LastCommit, m.Now)
	agePad := strings.Repeat(" ", max(ageW-cellLen(age), 0))

	row := fmt.Sprintf("%s%s   %s%s", name, pad, p.c(cLabel, age), agePad)
	if tip := c.Branch.Tip; tip != "" {
		row += "   " + p.c(cLabel, shortHash(tip))
	}
	if err, failed := m.Failed[c.Branch.Name]; failed {
		return row + "   " + p.c(cRed, "kept: "+firstLine(err.Error()))
	}
	if m.Applied {
		return row + "   " + p.c(cAccent, "deleted")
	}
	return row
}

func (p palette) tidySummary(w io.Writer, m TidyModel) {
	n := len(m.Plan.Candidates)
	forced := m.Plan.Forced()

	if !m.Applied {
		parts := fmt.Sprintf("%d %s would be deleted", n, plural(n, "branch", "branches"))
		if forced > 0 {
			parts += fmt.Sprintf(", %d needing -D", forced)
		}
		p.tidyLine(w, m.Width, cLabel, parts)
		if !m.Confirming {
			p.tidyLine(w, m.Width, cAmber, "nothing has been deleted — re-run with --apply to delete them")
		}
		return
	}

	deleted := n - len(m.Failed)
	p.tidyLine(w, m.Width, cAccent, fmt.Sprintf("%d %s deleted", deleted, plural(deleted, "branch", "branches")))
	if len(m.Failed) > 0 {
		p.tidyLine(w, m.Width, cRed, fmt.Sprintf("%d kept after errors", len(m.Failed)))
	}
	if deleted > 0 {
		// The hashes above are the whole undo story, so say so rather than
		// leaving the reader to work out that they can restore from them.
		p.tidyLine(w, m.Width, cLabel, "restore any of these with: git branch <name> <hash>")
	}
}

func (p palette) tidyProtected(w io.Writer, m TidyModel) {
	if len(m.Plan.Protected) == 0 {
		return
	}
	fmt.Fprintln(w)
	for _, prot := range m.Plan.Protected {
		p.tidyLine(w, m.Width, cLabel, fmt.Sprintf("kept %s (%s)", prot.Branch.Name, prot.Why))
	}
}

// tidyLine prints one indented, single-colour line, truncated to the terminal
// width. Colour is applied after truncation so an escape sequence is never cut
// in half.
func (p palette) tidyLine(w io.Writer, width int, color, text string) {
	if width > 0 && cellLen(text) > width-2 {
		text = truncate(text, width-2)
	}
	fmt.Fprintln(w, "  "+p.c(color, text))
}

// shortHash abbreviates a full commit hash for display, matching the 7
// characters git abbreviates to by default.
func shortHash(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}

// firstLine keeps a multi-line git error to one line so it fits a table row.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
