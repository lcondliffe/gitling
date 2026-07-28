// Package render draws the dashboard to an io.Writer.
//
// Color is 256-color ANSI only (no terminal-capability probing), so output is
// safe over SSH and in plain terminals. Greens are chosen to read on both light
// and dark backgrounds; when color is off, heatmap intensity is carried by glyph
// density instead of hue so no information is lost.
package render

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/lcondliffe/gitling/internal/aggregate"
	"github.com/lcondliffe/gitling/internal/forge"
	"github.com/lcondliffe/gitling/internal/gitdata"
)

// Model is everything the dashboard needs to draw; the cmd layer assembles it.
//
// Width is the detected terminal column count; 0 means unknown/unbounded, in
// which case rendering keeps its original fixed-width behavior (no
// truncation or elision) so piped output stays stable. Width never affects
// JSON output.
type Model struct {
	Vitals       gitdata.Vitals
	RangeLabel   string // e.g. "last 14 weeks"
	Days         []aggregate.DayCount
	TotalCommits int
	Streak       int
	Contributors []aggregate.Contributor
	Growth       aggregate.Growth
	HotFiles     []aggregate.FileChurn
	Recent       []gitdata.RecentCommit
	// PRs are the open pull requests on the forge hosting this repo; empty when
	// there is no forge, no forge CLI, or nothing open — the panel is dropped.
	PRs   []forge.PR
	Now   time.Time
	Width int
	// Layout selects the dashboard shape: LayoutAuto (default), LayoutWide, or
	// LayoutStack. The empty string means auto.
	Layout string
}

// GraphModel is the focused activity drill-down view. Width: see Model.
type GraphModel struct {
	RangeLabel   string
	Bucket       string
	Days         []aggregate.DayCount
	Buckets      []aggregate.PeriodCount
	TotalCommits int
	Streak       int
	Now          time.Time
	Width        int
}

// ChurnModel is the focused file-churn drill-down view. Width: see Model.
type ChurnModel struct {
	RangeLabel string
	Files      []aggregate.FileChurn
	Now        time.Time
	Width      int
}

// ContributorsModel is the focused contributor drill-down view. Width: see Model.
type ContributorsModel struct {
	RangeLabel   string
	Contributors []aggregate.Contributor
	Now          time.Time
	Width        int
}

// BranchesModel is the focused branch-overview drill-down view. Width: see Model.
type BranchesModel struct {
	Branches []gitdata.Branch
	Now      time.Time
	Width    int
}

// SGR color codes. cText ("") means the terminal's default foreground, which is
// the background-agnostic choice for body text.
const (
	cLabel  = "38;5;245" // section labels / muted text
	cAccent = "38;5;40"  // primary green
	cBright = "38;5;47"  // emphasis green
	cAmber  = "38;5;214" // dirty-tree warning
	cRed    = "38;5;203" // negative growth
)

// Per-level heatmap colors (0 = empty) and the no-color density ramp.
var (
	heatColors  = [5]string{"38;5;239", "38;5;22", "38;5;28", "38;5;34", "38;5;40"}
	heatGlyphs  = [5]string{"·", "░", "▒", "▓", "█"}
	chartBlocks = []rune(" ▁▂▃▄▅▆▇█") // 0..8 eighths of a cell
	cellFilled  = "■"
	cellToday   = "□" // hollow square marks today (the "distinct border")
	barFill     = "█"
	contribBarW = 22 // default/max bar width when the terminal width is unknown or generous
	minBarW     = 6  // never shrink bars below this; a thinner bar stops reading as a bar
)

type palette struct{ on bool }

func (p palette) c(code, s string) string {
	if !p.on || code == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Layout modes for the dashboard, selected by Model.Layout.
const (
	LayoutAuto  = "auto"  // two columns when the terminal is wide enough
	LayoutWide  = "wide"  // always two columns
	LayoutStack = "stack" // always one column
)

// minGridWidth is the narrowest terminal that gets the two-column grid. Below
// it, halving the width squeezes the contributor bars and file paths harder
// than the saved vertical space is worth.
const minGridWidth = 100

// gridGutter is the blank columns between the two grid columns.
const gridGutter = 1

// ValidLayout reports whether mode is a layout Dashboard understands.
func ValidLayout(mode string) bool {
	switch mode {
	case LayoutAuto, LayoutWide, LayoutStack, "":
		return true
	default:
		return false
	}
}

// Dashboard prints the panels as boxes, either stacked in one column or laid
// out in a two-column grid — see Model.Layout.
func Dashboard(w io.Writer, m Model, color bool) {
	p := palette{on: color}

	fmt.Fprintln(w)
	for _, line := range p.dashboard(m) {
		fmt.Fprintln(w, line)
	}
	fmt.Fprintln(w)
}

// useGrid decides between the grid and the stack. An unknown width (piped
// output) always stacks: without a real terminal there is no width to divide,
// and scripted consumers keep a stable shape.
func useGrid(m Model) bool {
	switch m.Layout {
	case LayoutWide:
		return true
	case LayoutStack:
		return false
	default:
		return m.Width >= minGridWidth
	}
}

func (p palette) dashboard(m Model) []string {
	if useGrid(m) {
		return p.dashboardGrid(m)
	}
	return p.dashboardStack(m)
}

// dashboardGrid lays the panels out in two columns, flowing top-to-bottom
// within each column so a short panel doesn't stretch its neighbour. Recent
// spans the full width underneath: its rows are far wider than half a terminal
// and would be truncated to uselessness in a column.
func (p palette) dashboardGrid(m Model) []string {
	// An unknown width (--layout=wide into a pipe) sizes each column to its own
	// content instead, the way the stacked layout does.
	natural := m.Width <= 0
	leftW := (m.Width - gridGutter) / 2
	rightW := m.Width - gridGutter - leftW
	panelLeftW, panelRightW, panelFullW := leftW-2, rightW-2, m.Width-2
	if natural {
		panelLeftW, panelRightW, panelFullW = 0, 0, 0
	}

	// Repo vitals and Recent span the full width, top and bottom: vitals is a
	// single status line that reads worse broken up, and Recent's rows are far
	// wider than half a terminal. The two columns hold the rest — hot files go
	// under the heatmap rather than under growth, which balances the heights
	// and keeps paths in the narrower column where they elide gracefully.
	vitals := p.vitalsLines(m, panelFullW)
	prs := p.prLines(m, panelFullW)
	recent := p.recentLines(m, panelFullW)
	heatmap := p.heatmapLines(m, panelLeftW)
	hot := p.hotFileLines(m, panelLeftW)
	contributors := p.contributorLines(m, panelRightW)
	growth := p.growthLines(m, panelRightW)

	fullW := m.Width - 2
	if natural {
		// +1 keeps content off the border, +2 covers the border itself.
		leftW = contentWidth(heatmap, hot) + 3
		rightW = contentWidth(contributors, growth) + 3
		fullW = max(contentWidth(vitals, prs, recent)+1, leftW+gridGutter+rightW-2)
	}

	leftBoxes := [][]string{
		p.box(titleWith("Activity", m.RangeLabel), heatmap, leftW-2),
	}
	if len(hot) > 0 {
		leftBoxes = append(leftBoxes, p.box(titleWith("Hot files", ""), hot, leftW-2))
	}

	right := stackBoxes(
		p.box(titleWith("Top contributors", ""), contributors, rightW-2),
		p.box(titleWith("Codebase growth", "6mo"), growth, rightW-2),
	)

	out := p.box(titleWith("Repo", ""), vitals, fullW)
	out = append(out, sideBySide(stackBoxes(leftBoxes...), right, leftW, gridGutter)...)
	if len(prs) > 0 {
		out = append(out, p.box(prTitle(m), prs, fullW)...)
	}
	if len(recent) > 0 {
		out = append(out, p.box(recentTitle(m), recent, fullW)...)
	}
	return out
}

// dashboardStack lays the panels out in a single column of full-width boxes.
// When the width is unknown, every box is sized to the widest content line so
// the column still has a straight right edge.
func (p palette) dashboardStack(m Model) []string {
	innerW := m.Width - 2
	panelW := innerW
	if m.Width <= 0 {
		panelW = 0 // unbounded: panels render at their natural width
	}

	vitals := p.vitalsLines(m, panelW)
	heatmap := p.heatmapLines(m, panelW)
	prs := p.prLines(m, panelW)
	recent := p.recentLines(m, panelW)
	contributors := p.contributorLines(m, panelW)
	growth := p.growthLines(m, panelW)
	hot := p.hotFileLines(m, panelW)

	if m.Width <= 0 {
		// One trailing column keeps content off the right border.
		innerW = contentWidth(vitals, heatmap, prs, recent, contributors, growth, hot) + 1
	}

	boxes := [][]string{
		p.box(titleWith("Repo", ""), vitals, innerW),
		p.box(titleWith("Activity", m.RangeLabel), heatmap, innerW),
	}
	if len(prs) > 0 {
		boxes = append(boxes, p.box(prTitle(m), prs, innerW))
	}
	if len(recent) > 0 {
		boxes = append(boxes, p.box(recentTitle(m), recent, innerW))
	}
	boxes = append(boxes,
		p.box(titleWith("Top contributors", ""), contributors, innerW),
		p.box(titleWith("Codebase growth", "6mo"), growth, innerW),
	)
	if len(hot) > 0 {
		boxes = append(boxes, p.box(titleWith("Hot files", ""), hot, innerW))
	}
	return stackBoxes(boxes...)
}

// titleWith builds a box title: the label uppercased as section headers always
// were, with its qualifier left in sentence case, e.g. "ACTIVITY · last 14 weeks".
func titleWith(label, suffix string) string {
	label = strings.ToUpper(label)
	if suffix == "" {
		return label
	}
	return label + " · " + suffix
}

func prTitle(m Model) string {
	return titleWith("Open PRs", strconv.Itoa(len(m.PRs)))
}

func recentTitle(m Model) string {
	return titleWith("Recent", fmt.Sprintf("%d %s", len(m.Recent), plural(len(m.Recent), "commit", "commits")))
}

// The *Lines helpers render one panel's body (no header, no surrounding blank
// lines) at the given width, ready to be framed by box.
func (p palette) vitalsLines(m Model, width int) []string {
	return capture(func(w io.Writer) { p.vitals(w, m, width) })
}

func (p palette) heatmapLines(m Model, width int) []string {
	m.Width = width
	return capture(func(w io.Writer) { p.heatmap(w, m) })
}

func (p palette) contributorLines(m Model, width int) []string {
	return capture(func(w io.Writer) { p.contributors(w, m.Contributors, width) })
}

func (p palette) growthLines(m Model, width int) []string {
	return capture(func(w io.Writer) { p.growth(w, m.Growth) })
}

func (p palette) hotFileLines(m Model, width int) []string {
	return capture(func(w io.Writer) { p.hotFiles(w, m.HotFiles, width) })
}

func (p palette) prLines(m Model, width int) []string {
	if len(m.PRs) == 0 {
		return nil
	}
	return capture(func(w io.Writer) { p.prs(w, m.PRs, m.Now, width) })
}

func (p palette) recentLines(m Model, width int) []string {
	if len(m.Recent) == 0 {
		return nil
	}
	return capture(func(w io.Writer) { p.recent(w, m.Recent, m.Now, width) })
}

// capture collects what a panel renderer writes, one string per line.
func capture(fn func(io.Writer)) []string {
	var buf strings.Builder
	fn(&buf)
	s := strings.TrimRight(buf.String(), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// Graph prints a focused activity drill-down: the familiar heatmap, a taller
// bucketed activity chart, and exact bucket totals for scripting-by-eyeball.
func Graph(w io.Writer, m GraphModel, color bool) {
	p := palette{on: color}

	fmt.Fprintln(w)
	p.header(w, "Activity graph", m.RangeLabel+" · "+m.Bucket)
	fmt.Fprintln(w)
	p.heatmap(w, Model{Days: m.Days, TotalCommits: m.TotalCommits, Streak: m.Streak, Now: m.Now, Width: m.Width})

	if chart := p.activityChart(m.Buckets, activityChartHeight); len(chart) > 0 {
		fmt.Fprintln(w)
		for _, line := range chart {
			fmt.Fprintln(w, "  "+line)
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "  "+p.c(cLabel, "counts"))
	// Empty buckets are dropped and the rest are packed across the terminal:
	// a day-bucketed quarter is ~90 rows, nearly all of them zeros the heatmap
	// and the chart above have already shown.
	cells := make([]string, 0, len(m.Buckets))
	cellW := 0
	for _, b := range m.Buckets {
		if b.Count == 0 {
			continue
		}
		cell := p.c(cLabel, periodLabel(b, m.Bucket)) + " " + strconv.Itoa(b.Count)
		if n := visibleLen(cell); n > cellW {
			cellW = n
		}
		cells = append(cells, cell)
	}
	if len(cells) == 0 {
		fmt.Fprintln(w, "    "+p.c(cLabel, "no commits in range"))
		fmt.Fprintln(w)
		return
	}
	// An unknown width (piped) stays one per line, as the other panels do, so
	// `gitling graph | grep` keeps working.
	lines := cells
	if m.Width > 0 {
		for i, c := range cells {
			cells[i] = padVisible(c, cellW)
		}
		lines = packParts(cells, m.Width-4, 3)
	}
	for _, line := range lines {
		fmt.Fprintln(w, "    "+strings.TrimRight(line, " "))
	}
	fmt.Fprintln(w)
}

// Contributors prints a focused contributor drill-down: every author with
// commits in range, ranked, with a bar and exact counts — the full list behind
// the dashboard's top-5 panel. It reuses the dashboard's contributor renderer,
// then adds a totals summary.
func Contributors(w io.Writer, m ContributorsModel, color bool) {
	p := palette{on: color}

	fmt.Fprintln(w)
	p.header(w, "Contributors", m.RangeLabel)
	fmt.Fprintln(w)
	p.contributors(w, m.Contributors, m.Width)

	if len(m.Contributors) > 0 {
		total := 0
		for _, c := range m.Contributors {
			total += c.Commits
		}
		fmt.Fprintln(w)
		summary := fmt.Sprintf("%d %s · %d %s",
			len(m.Contributors), plural(len(m.Contributors), "contributor", "contributors"),
			total, plural(total, "commit", "commits"))
		fmt.Fprintln(w, "  "+p.c(cLabel, summary))
	}
	fmt.Fprintln(w)
}

// Churn prints a focused file-churn drill-down: every file touched in range,
// ranked by the number of commits touching it, with a bar and exact counts.
func Churn(w io.Writer, m ChurnModel, color bool) {
	p := palette{on: color}

	fmt.Fprintln(w)
	p.header(w, "File churn", m.RangeLabel)
	fmt.Fprintln(w)

	if len(m.Files) == 0 {
		fmt.Fprintln(w, "  "+p.c(cLabel, "no commits in range"))
		fmt.Fprintln(w)
		return
	}

	countW := 0
	for _, f := range m.Files {
		if n := len(strconv.Itoa(f.Commits)); n > countW {
			countW = n
		}
	}
	// "  " + bar + "   " + count + "   " precedes the path.
	barW := barWidthFor(m.Width, 2+3+countW+3)
	pathW := pathBudget(m.Width, 2+barW+3+countW+3)
	// Files arrive sorted by descending commit count, so the first is the peak.
	maxC := m.Files[0].Commits
	for _, f := range m.Files {
		count := p.c(cLabel, fmt.Sprintf("%*d", countW, f.Commits))
		fmt.Fprintf(w, "  %s   %s   %s\n", p.bar(f.Commits, maxC, barW), count, elidePath(f.Path, pathW))
	}

	fmt.Fprintln(w)
	summary := fmt.Sprintf("%d %s touched", len(m.Files), plural(len(m.Files), "file", "files"))
	fmt.Fprintln(w, "  "+p.c(cLabel, summary))
	fmt.Fprintln(w)
}

// Branches prints a focused branch overview: local branches ranked by most
// recent commit, each with ahead/behind vs its upstream (or the default branch),
// how long ago the tip was committed, and the tip author.
func Branches(w io.Writer, m BranchesModel, color bool) {
	p := palette{on: color}

	fmt.Fprintln(w)
	p.header(w, "Branches", fmt.Sprintf("%d %s", len(m.Branches), plural(len(m.Branches), "branch", "branches")))
	fmt.Fprintln(w)

	if len(m.Branches) == 0 {
		fmt.Fprintln(w, "  "+p.c(cLabel, "no local branches"))
		fmt.Fprintln(w)
		return
	}

	nameW, trackW, dateW := 0, 0, 0
	for _, b := range m.Branches {
		if n := cellLen(b.Name); n > nameW {
			nameW = n
		}
		if n := cellLen(branchTrack(b)); n > trackW {
			trackW = n
		}
		if n := cellLen(humanAgo(b.LastCommit, m.Now)); n > dateW {
			dateW = n
		}
	}
	maxNameW := 32
	if m.Width > 0 {
		// marker(2) + name + gap(3) + track + gap(3) + date + gap(3) precedes
		// the author; whatever's left caps the name column, bounded so it
		// never collapses to unreadable.
		avail := m.Width - 2 - 3 - trackW - 3 - dateW - 3
		if avail < 8 {
			avail = 8
		}
		if avail < maxNameW {
			maxNameW = avail
		}
	}
	if nameW > maxNameW {
		nameW = maxNameW
	}

	for _, b := range m.Branches {
		marker := "  "
		if b.IsHead {
			marker = p.c(cAccent, "*") + " "
		}
		name := truncate(b.Name, nameW)
		namePad := strings.Repeat(" ", nameW-cellLen(name))
		nameCol := name
		if b.IsHead {
			nameCol = p.c(cBright, name)
		}
		track := branchTrack(b)
		trackPad := strings.Repeat(" ", trackW-cellLen(track))
		date := humanAgo(b.LastCommit, m.Now)
		datePad := strings.Repeat(" ", dateW-cellLen(date))

		line := fmt.Sprintf("%s%s%s   %s%s   %s%s   %s",
			marker, nameCol, namePad,
			p.branchTrack(b), trackPad,
			p.c(cLabel, date), datePad,
			p.c(cLabel, b.LastAuthor))
		// Note the comparison base only when it's the default-branch fallback
		// (an upstream is implied and doesn't need spelling out).
		if b.HasCompare && b.Upstream == "" && b.CompareRef != "" {
			line += "   " + p.c(cLabel, "vs "+b.CompareRef)
		}
		fmt.Fprintln(w, "  "+strings.TrimRight(line, " "))
	}
	fmt.Fprintln(w)
}

// branchTrack is the plain (uncolored) ahead/behind cell, used for width.
func branchTrack(b gitdata.Branch) string {
	switch {
	case b.Gone:
		return "gone"
	case b.HasCompare:
		return fmt.Sprintf("↑%d ↓%d", b.Ahead, b.Behind)
	default:
		return "—"
	}
}

// branchTrack (method) is the colored version of the same cell; it has the same
// visible width as the plain form so column padding still lines up.
func (p palette) branchTrack(b gitdata.Branch) string {
	switch {
	case b.Gone:
		return p.c(cAmber, "gone")
	case b.HasCompare:
		ahead := p.c(cLabel, fmt.Sprintf("↑%d", b.Ahead))
		if b.Ahead > 0 {
			ahead = p.c(cAccent, fmt.Sprintf("↑%d", b.Ahead))
		}
		return ahead + " " + p.c(cLabel, fmt.Sprintf("↓%d", b.Behind))
	default:
		return p.c(cLabel, "—")
	}
}

// humanAgo renders a compact "time since" label for a branch tip.
func humanAgo(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/24/30))
	default:
		return fmt.Sprintf("%dy ago", int(d.Hours()/24/365))
	}
}

func (p palette) header(w io.Writer, label, suffix string) {
	s := strings.ToUpper(label)
	if suffix != "" {
		s += "  ·  " + suffix
	}
	fmt.Fprintln(w, p.c(cLabel, s))
}

// vitals prints the one-line repo status bar. When the parts don't fit the
// available width they wrap onto further lines rather than being clipped: every
// field here is a distinct fact, so losing the tail loses information.
// staleFetch is how old the last fetch has to be before the panel calls it out.
// Ahead/behind is measured against remote-tracking refs, so past roughly a day
// "↓0" stops meaning "nothing upstream" and starts meaning "nothing I know of".
const staleFetch = 24 * time.Hour

func (p palette) vitals(w io.Writer, m Model, width int) {
	v := m.Vitals
	// The status dot escalates: green clean, amber for uncommitted work, red
	// when the repo is mid-operation and needs finishing before anything else.
	dotColor := cAccent
	switch {
	case v.Operation.InProgress() || v.Conflicts > 0:
		dotColor = cRed
	case v.DirtyFiles > 0:
		dotColor = cAmber
	}

	parts := []string{p.c(dotColor, "●") + " " + p.c(cBright, v.Branch)}
	// An interrupted rebase/merge outranks everything else on the line, so it
	// goes first, right after the branch.
	if v.Operation.InProgress() {
		parts = append(parts, p.c(cRed, operationLabel(v.Operation)))
	}
	if v.HasUpstream {
		parts = append(parts, fmt.Sprintf("%s%d %s%d",
			p.c(cAccent, "↑"), v.Ahead, p.c(cLabel, "↓"), v.Behind))
	}
	if !v.LastFetch.IsZero() {
		fetchColor := cLabel
		if m.Now.Sub(v.LastFetch) >= staleFetch {
			fetchColor = cAmber
		}
		parts = append(parts, p.c(fetchColor, "fetched "+humanAgo(v.LastFetch, m.Now)))
	}
	parts = append(parts, p.dirtyParts(v)...)
	if v.StashCount > 0 {
		stash := fmt.Sprintf("%d %s", v.StashCount, plural(v.StashCount, "stash", "stashes"))
		if !v.OldestStash.IsZero() {
			// "(oldest 7mo ago)" reads worse than "(oldest 7mo)" next to a count.
			stash += fmt.Sprintf(" (oldest %s)", strings.TrimSuffix(humanAgo(v.OldestStash, m.Now), " ago"))
		}
		parts = append(parts, p.c(cLabel, stash))
	}

	// Branch health gets its own line when there is anything to act on, because
	// it is the one group whose parts only make sense read together.
	branch := p.branchParts(v)
	if len(branch) == 1 {
		parts = append(parts, branch...)
		branch = nil
	}
	for _, line := range packParts(parts, width-2, 3) {
		fmt.Fprintln(w, "  "+line)
	}
	for _, line := range packParts(branch, width-2, 3) {
		fmt.Fprintln(w, "  "+line)
	}
}

// operationLabel names an in-progress operation, with its position in the
// sequence when git tracks one.
func operationLabel(op gitdata.Operation) string {
	if op.Total > 0 {
		return fmt.Sprintf("⚠ %s %d/%d", op.Kind, op.Step, op.Total)
	}
	return "⚠ " + op.Kind + " in progress"
}

// dirtyParts breaks the working tree down by what kind of change each file
// carries, which says far more than a single count about whether you were
// mid-commit. Staged and modified overlap by design (see gitdata.Vitals), so
// no total is shown alongside them; empty categories are dropped entirely.
func (p palette) dirtyParts(v gitdata.Vitals) []string {
	if v.DirtyFiles == 0 {
		return []string{p.c(cLabel, "clean")}
	}
	var parts []string
	if v.Conflicts > 0 {
		parts = append(parts, p.c(cRed, fmt.Sprintf("%d %s", v.Conflicts, plural(v.Conflicts, "conflict", "conflicts"))))
	}
	for _, part := range []struct {
		n     int
		label string
	}{
		{v.Staged, "staged"},
		{v.Modified, "modified"},
		{v.Untracked, "untracked"},
	} {
		if part.n > 0 {
			parts = append(parts, p.c(cLabel, fmt.Sprintf("%d %s", part.n, part.label)))
		}
	}
	if len(parts) == 0 {
		// Every entry fell outside the breakdown (a status code we don't
		// classify); fall back to the bare total rather than showing nothing.
		parts = append(parts, p.c(cLabel, fmt.Sprintf("%d dirty", v.DirtyFiles)))
	}
	return parts
}

// branchParts renders the branch count plus the cleanup-candidate counts. A
// repo with nothing to clean up gets just the count, exactly as before.
func (p palette) branchParts(v gitdata.Vitals) []string {
	parts := []string{p.c(cLabel, fmt.Sprintf("%d %s", v.BranchCount, plural(v.BranchCount, "branch", "branches")))}
	if v.MergedBranches > 0 {
		parts = append(parts, p.c(cLabel, fmt.Sprintf("%d merged", v.MergedBranches)))
	}
	if v.GoneBranches > 0 {
		parts = append(parts, p.c(cAmber, fmt.Sprintf("%d gone", v.GoneBranches)))
	}
	if v.StaleBranches > 0 {
		parts = append(parts, p.c(cAmber, fmt.Sprintf("%d stale >%dd", v.StaleBranches, v.StaleAfterDays)))
	}
	return parts
}

// packParts greedily groups parts into lines at most width visible columns
// wide, joined by sep spaces. A width of 0 or less means unbounded: everything
// goes on one line, as it always did when the terminal width is unknown.
func packParts(parts []string, width, sep int) []string {
	gap := strings.Repeat(" ", sep)
	if len(parts) == 0 {
		return nil // no parts is no lines, not one blank one
	}
	if width <= 0 {
		return []string{strings.Join(parts, gap)}
	}
	var lines []string
	var cur string
	curLen := 0
	for _, part := range parts {
		n := visibleLen(part)
		switch {
		case cur == "":
			cur, curLen = part, n
		case curLen+sep+n <= width:
			cur += gap + part
			curLen += sep + n
		default:
			lines = append(lines, cur)
			cur, curLen = part, n
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

type cell struct {
	present bool
	count   int
	today   bool
}

// buildGrid lays days out as 7 rows (Sun..Sat) by N week-columns, oldest left.
// It returns the grid, its column count, and the max daily count for scaling.
func buildGrid(days []aggregate.DayCount, now time.Time) (grid [7][]cell, cols, max int) {
	if len(days) == 0 {
		return grid, 0, 0
	}
	since, until := days[0].Date, days[len(days)-1].Date
	gridStart := since.AddDate(0, 0, -int(since.Weekday())) // back to Sunday
	cols = aggregate.DaysBetween(gridStart, until)/7 + 1
	for r := range grid {
		grid[r] = make([]cell, cols)
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, dc := range days {
		col := aggregate.DaysBetween(gridStart, dc.Date) / 7
		row := int(dc.Date.Weekday())
		if col < 0 || col >= cols {
			continue
		}
		grid[row][col] = cell{present: true, count: dc.Count, today: dc.Date.Equal(today)}
		if dc.Count > max {
			max = dc.Count
		}
	}
	return grid, cols, max
}

func (p palette) heatmap(w io.Writer, m Model) {
	grid, cols, max := buildGrid(m.Days, m.Now)
	// Each column costs 2 chars (glyph + space) after a 2-char indent. When
	// width is known and the full range doesn't fit, show the most recent
	// columns (drop the oldest) rather than overflowing the line.
	startCol := 0
	if m.Width > 0 {
		maxCols := (m.Width - 2) / 2
		if maxCols < 1 {
			maxCols = 1
		}
		if cols > maxCols {
			startCol = cols - maxCols
		}
	}
	for r := 0; r < 7; r++ {
		var b strings.Builder
		b.WriteString("  ")
		for col := startCol; col < cols; col++ {
			b.WriteString(p.cellGlyph(grid[r][col], max))
			b.WriteByte(' ')
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
	summary := fmt.Sprintf("%d commits in range · streak: %d days", m.TotalCommits, m.Streak)
	fmt.Fprintln(w, "  "+p.c(cLabel, summary))
}

func (p palette) cellGlyph(c cell, max int) string {
	if !c.present {
		return " "
	}
	lvl := level(c.count, max)
	if c.today {
		code := heatColors[lvl]
		if lvl == 0 {
			code = cAccent // keep today visible even with no commits
		}
		return p.c(code, cellToday)
	}
	if p.on {
		return p.c(heatColors[lvl], cellFilled)
	}
	return heatGlyphs[lvl]
}

func level(count, max int) int {
	if count <= 0 {
		return 0
	}
	if max <= 0 {
		return 1
	}
	switch q := float64(count) / float64(max); {
	case q > 0.75:
		return 4
	case q > 0.5:
		return 3
	case q > 0.25:
		return 2
	default:
		return 1
	}
}

func (p palette) contributors(w io.Writer, cs []aggregate.Contributor, width int) {
	if len(cs) == 0 {
		fmt.Fprintln(w, "  "+p.c(cLabel, "no commits in range"))
		return
	}
	nameW, countW := 0, 0
	for _, c := range cs {
		if n := cellLen(c.Name); n > nameW {
			nameW = n
		}
		if n := len(strconv.Itoa(c.Commits)); n > countW {
			countW = n
		}
	}
	if maxName := maxNameWidth(width, countW); nameW > maxName {
		nameW = maxName
	}
	// "  " + name + "   " + bar + "   " + count precedes the newline.
	barW := barWidthFor(width, 2+nameW+3+3+countW)
	maxC := cs[0].Commits
	for _, c := range cs {
		name := truncate(c.Name, nameW)
		pad := strings.Repeat(" ", nameW-cellLen(name))
		count := p.c(cLabel, fmt.Sprintf("%*d", countW, c.Commits))
		fmt.Fprintf(w, "  %s%s   %s   %s\n", name, pad, p.bar(c.Commits, maxC, barW), count)
	}
}

// bar draws one fill-only bar w columns wide: a green run scaled against max,
// padded with spaces (no track). Compact enough to keep rows tight, but with no
// dim block to stack up; the space padding still lines the counts up. A nonzero
// count always gets at least a sliver, so every row reads as present.
func (p palette) bar(count, max, w int) string {
	filled := 0
	if max > 0 {
		filled = int(float64(count)/float64(max)*float64(w) + 0.5)
	}
	if filled < 1 {
		filled = 1
	}
	if filled > w {
		filled = w
	}
	return p.c(cAccent, strings.Repeat(barFill, filled)) + strings.Repeat(" ", w-filled)
}

// recent draws the newest commits at the tip of HEAD, one per line:
//
//	<short hash>  <#PR>  <subject>  <author>  <how long ago>
//
// The PR column is dropped entirely when nothing in the list carries a
// pull-request number, so repos that don't merge through PRs don't pay for a
// column of blanks. The subject is the flexible column: it absorbs whatever
// width is left after the fixed ones, and is truncated (never wrapped) so each
// commit stays on exactly one line.
//
// cs must be non-empty; the caller drops the whole panel when there is nothing
// to show rather than printing an empty-state line.
func (p palette) recent(w io.Writer, cs []gitdata.RecentCommit, now time.Time, width int) {
	hashW, prW, authorW, agoW, subjectW := 0, 0, 0, 0, 0
	for _, c := range cs {
		if n := cellLen(c.Short); n > hashW {
			hashW = n
		}
		if n := cellLen(c.Subject); n > subjectW {
			subjectW = n
		}
		if n := cellLen(prLabel(c)); n > prW {
			prW = n
		}
		if n := cellLen(c.Author); n > authorW {
			authorW = n
		}
		if n := cellLen(humanAgo(c.Time, now)); n > agoW {
			agoW = n
		}
	}
	if authorW > 16 {
		authorW = 16
	}

	// "  " + hash + "  " [+ pr + "  "] + subject + "  " + author + "  " + ago.
	overhead := 2 + hashW + 2 + 2 + authorW + 2 + agoW
	if prW > 0 {
		overhead += prW + 2
	}
	// The subject column is as wide as its longest entry, shrunk to fit the
	// terminal when the width is known. Width 0 (unknown/piped) keeps the full
	// subjects, as the other panels do.
	if width > 0 {
		const minSubjectW = 12
		if avail := max(width-overhead, minSubjectW); avail < subjectW {
			subjectW = avail
		}
	}

	for _, c := range cs {
		var b strings.Builder
		b.WriteString("  " + p.c(cLabel, c.Short) + strings.Repeat(" ", hashW-cellLen(c.Short)) + "  ")
		if prW > 0 {
			pr := prLabel(c)
			code := cAccent
			if pr == "" {
				pr, code = "·", cLabel // a placeholder keeps the subject column aligned
			}
			b.WriteString(p.c(code, pr) + strings.Repeat(" ", prW-cellLen(pr)) + "  ")
		}
		subject := truncate(c.Subject, subjectW)
		b.WriteString(subject + strings.Repeat(" ", subjectW-cellLen(subject)) + "  ")
		author := truncate(c.Author, authorW)
		b.WriteString(p.c(cLabel, author) + strings.Repeat(" ", authorW-cellLen(author)) + "  ")
		b.WriteString(p.c(cLabel, humanAgo(c.Time, now)))
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
}

// prs draws the open pull requests, one per line:
//
//	#123  fix: the thing                    alice  2h ago
//
// Same shape as recent, and the same trade-off: the title is the flexible
// column and is truncated rather than wrapped so a PR stays on one line.
//
// prs must be non-empty; the caller drops the whole panel when there is
// nothing open.
func (p palette) prs(w io.Writer, prs []forge.PR, now time.Time, width int) {
	numW, authorW, agoW, titleW := 0, 0, 0, 0
	for _, pr := range prs {
		if n := runeLen(prNumber(pr)); n > numW {
			numW = n
		}
		if n := runeLen(prTitleText(pr)); n > titleW {
			titleW = n
		}
		if n := runeLen(pr.Author); n > authorW {
			authorW = n
		}
		if n := runeLen(humanAgo(pr.Updated, now)); n > agoW {
			agoW = n
		}
	}
	if authorW > 16 {
		authorW = 16
	}

	// "  " + number + "  " + title + "  " + author + "  " + ago.
	overhead := 2 + numW + 2 + 2 + authorW + 2 + agoW
	if width > 0 {
		const minTitleW = 12
		if avail := max(width-overhead, minTitleW); avail < titleW {
			titleW = avail
		}
	}

	for _, pr := range prs {
		var b strings.Builder
		num := prNumber(pr)
		b.WriteString("  " + p.c(cAccent, num) + strings.Repeat(" ", numW-runeLen(num)) + "  ")
		title := truncate(prTitleText(pr), titleW)
		b.WriteString(title + strings.Repeat(" ", titleW-runeLen(title)) + "  ")
		author := truncate(pr.Author, authorW)
		b.WriteString(p.c(cLabel, author) + strings.Repeat(" ", authorW-runeLen(author)) + "  ")
		b.WriteString(p.c(cLabel, humanAgo(pr.Updated, now)))
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
}

func prNumber(pr forge.PR) string { return "#" + strconv.Itoa(pr.Number) }

// prTitleText marks drafts inline rather than in a column of their own: most
// lists have none, and an empty column costs every row.
func prTitleText(pr forge.PR) string {
	if pr.Draft {
		return "draft: " + pr.Title
	}
	return pr.Title
}

// prLabel is the plain (uncolored) pull-request cell, empty when the commit
// carries no discoverable PR number.
func prLabel(c gitdata.RecentCommit) string {
	if c.PR <= 0 {
		return ""
	}
	return "#" + strconv.Itoa(c.PR)
}

// maxNameWidth is how much of a contributor's name may be shown. Names are
// capped so a single long one can't push the bars off the line, but the cap
// grows to use whatever a wide panel has spare after a full-length bar — a
// truncated "Margaret Hamilt…" beside 20 empty columns helps nobody.
//
// An unknown width (piped output) keeps the fixed cap, so redirected output
// stays stable regardless of who has committed.
func maxNameWidth(width, countW int) int {
	const base = 16
	if width <= 0 {
		return base
	}
	// "  " + name + "   " + bar + "   " + count.
	if avail := width - 2 - 3 - contribBarW - 3 - countW; avail > base {
		return avail
	}
	return base
}

func (p palette) growth(w io.Writer, g aggregate.Growth) {
	var pct string
	switch {
	case !g.HasPct:
		pct = p.c(cLabel, "·")
	case g.Pct >= 0.5:
		pct = p.c(cAccent, fmt.Sprintf("▲ %.0f%%", g.Pct))
	case g.Pct <= -0.5:
		pct = p.c(cRed, fmt.Sprintf("▼ %.0f%%", -g.Pct))
	default:
		pct = p.c(cLabel, "≈ 0%") // 6mo baseline exists but essentially flat
	}
	fmt.Fprintf(w, "  %s LOC  %s\n", p.c(cBright, humanInt(g.TotalLOC)), pct)

	if chart := p.growthChart(g.Spark, growthChartHeight); len(chart) > 0 {
		for _, line := range chart {
			fmt.Fprintln(w, "  "+line)
		}
	}
}

// hotFiles lists the files with the most commits against them. Counts lead so
// they stay aligned and the (often long) paths can be elided from the middle
// without disturbing the column.
func (p palette) hotFiles(w io.Writer, hot []aggregate.FileChurn, width int) {
	if len(hot) == 0 {
		return
	}
	countW := 0
	for _, f := range hot {
		if n := len(strconv.Itoa(f.Commits)); n > countW {
			countW = n
		}
	}
	// "  " + count + "   " precedes the path.
	pathW := pathBudget(width, 2+countW+3)
	for _, f := range hot {
		count := p.c(cLabel, fmt.Sprintf("%*d", countW, f.Commits))
		fmt.Fprintf(w, "  %s   %s\n", count, elidePath(f.Path, pathW))
	}
}

const growthChartHeight = 5   // rows tall; height*8 gives the vertical resolution
const activityChartHeight = 8 // taller drill-down chart for the graph view

// growthChart renders vals as a vertical bar chart growthChartHeight rows tall,
// min-max normalized so the peak reaches the top and the flat start sits near
// the floor. Using multiple rows (rather than a single-line sparkline) gives the
// trend real vertical resolution — the climb is actually visible. Returns one
// string per row, top first.
func (p palette) growthChart(vals []int, height int) []string {
	if len(vals) == 0 || height < 1 {
		return nil
	}
	min, max := vals[0], vals[0]
	for _, v := range vals {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	span := max - min
	// Height of each column measured in eighths of a cell (0..height*8).
	eighths := make([]int, len(vals))
	for i, v := range vals {
		if span > 0 {
			eighths[i] = int(float64(v-min)/float64(span)*float64(height*8) + 0.5)
		} else {
			eighths[i] = 4 // flat history: a thin baseline rather than a full block
		}
	}
	return p.renderColumns(eighths, height)
}

// renderColumns draws a column chart from per-column heights measured in
// eighths of a cell (0..height*8), one string per row, top row first. The
// callers differ only in how they scale their values into those eighths.
func (p palette) renderColumns(eighths []int, height int) []string {
	lines := make([]string, height)
	for r := 0; r < height; r++ { // r == 0 is the top row
		cellBottom := (height - 1 - r) * 8
		var b strings.Builder
		for _, e := range eighths {
			fill := e - cellBottom
			if fill < 0 {
				fill = 0
			} else if fill > 8 {
				fill = 8
			}
			b.WriteRune(chartBlocks[fill])
		}
		lines[r] = p.c(cAccent, strings.TrimRight(b.String(), " "))
	}
	return lines
}

func (p palette) activityChart(buckets []aggregate.PeriodCount, height int) []string {
	vals := make([]int, len(buckets))
	for i, b := range buckets {
		vals[i] = b.Count
	}
	return p.barChart(vals, height)
}

func (p palette) barChart(vals []int, height int) []string {
	if len(vals) == 0 || height < 1 {
		return nil
	}
	max := vals[0]
	for _, v := range vals {
		if v > max {
			max = v
		}
	}
	if max <= 0 {
		return nil
	}
	eighths := make([]int, len(vals))
	for i, v := range vals {
		eighths[i] = int(float64(v)/float64(max)*float64(height*8) + 0.5)
	}
	return p.renderColumns(eighths, height)
}

func periodLabel(b aggregate.PeriodCount, bucket string) string {
	switch bucket {
	case "week":
		return fmt.Sprintf("%s..%s", b.Start.Format("2006-01-02"), b.End.Format("2006-01-02"))
	case "month":
		return b.Start.Format("2006-01")
	default:
		return b.Start.Format("2006-01-02")
	}
}

func humanInt(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.Itoa(n)
	var out []byte
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// truncate shortens s to at most max terminal cells, marking the cut with an
// ellipsis. Like clipVisible it drops a double-width cluster whole rather than
// splitting it, so the result may be one cell short of max. A max of 1 yields
// the bare ellipsis (a wide rune would not fit in the cell), and a
// non-positive max yields the empty string.
func truncate(s string, max int) string {
	if cellLen(s) <= max {
		return s
	}
	if max <= 0 {
		return ""
	}
	return head(s, max-1) + "…"
}

// barWidthFor scales a fill-bar to fit within width, given overhead columns
// already spent on the rest of the row (indent, name/count columns, gaps).
// width <= 0 means unknown/unbounded, so the bar keeps its original fixed
// width (today's behavior; piped output stays stable). Otherwise the bar
// never grows past contribBarW and never shrinks below minBarW, so it keeps
// reading as a bar even on very narrow terminals (rows may still overflow —
// this only guards against negative or zero widths).
func barWidthFor(width, overhead int) int {
	if width <= 0 {
		return contribBarW
	}
	avail := width - overhead
	if avail > contribBarW {
		return contribBarW
	}
	if avail < minBarW {
		return minBarW
	}
	return avail
}

// pathBudget derives the max path length for elidePath from a total width
// and the columns already spent on the rest of the row. It returns 0 (no
// limit) when width is unknown/unbounded; otherwise it never returns
// less than a minimum viable path length, since the row overhead alone
// can exceed width on very narrow terminals.
func pathBudget(width, overhead int) int {
	if width <= 0 {
		return 0
	}
	const minPathW = 4
	b := width - overhead
	if b < minPathW {
		return minPathW
	}
	return b
}

// elidePath shortens path to at most maxLen terminal cells, keeping the final
// path segment (the filename) fully visible and eliding from the middle of the
// leading directory portion — long terraform-style paths stay readable
// instead of just running off the line. maxLen <= 0 means unbounded: the
// path is returned unchanged (used when the terminal width is unknown, so
// piped output keeps today's behavior).
func elidePath(path string, maxLen int) string {
	if maxLen <= 0 {
		return path
	}
	if cellLen(path) <= maxLen {
		return path
	}
	const ellipsis = "…"
	base := path
	if idx := strings.LastIndexByte(path, '/'); idx >= 0 {
		base = path[idx+1:]
	}
	if cellLen(base) >= maxLen-1 {
		// Even the filename alone doesn't fit; truncate its tail.
		return truncate(base, maxLen)
	}
	headBudget := maxLen - cellLen(base) - cellLen(ellipsis)
	if headBudget < 0 {
		headBudget = 0
	}
	return head(path, headBudget) + ellipsis + base
}

// head returns the longest prefix of s fitting in w terminal cells. A
// double-width cluster that would straddle the limit is dropped whole.
func head(s string, w int) string {
	n := 0
	for i := 0; i < len(s); {
		cw, next := stepCell(s, i)
		if n+cw > w {
			return s[:i]
		}
		n += cw
		i = next
	}
	return s
}
