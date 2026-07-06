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
	"github.com/lcondliffe/gitling/internal/gitdata"
)

// Model is everything the dashboard needs to draw; the cmd layer assembles it.
type Model struct {
	Vitals       gitdata.Vitals
	RangeLabel   string // e.g. "last 14 weeks"
	Days         []aggregate.DayCount
	TotalCommits int
	Streak       int
	Contributors []aggregate.Contributor
	Growth       aggregate.Growth
	HotFiles     []aggregate.FileChurn
	Now          time.Time
}

// GraphModel is the focused activity drill-down view.
type GraphModel struct {
	RangeLabel   string
	Bucket       string
	Days         []aggregate.DayCount
	Buckets      []aggregate.PeriodCount
	TotalCommits int
	Streak       int
	Now          time.Time
}

// ChurnModel is the focused file-churn drill-down view.
type ChurnModel struct {
	RangeLabel string
	Files      []aggregate.FileChurn
	Now        time.Time
}

// ContributorsModel is the focused contributor drill-down view.
type ContributorsModel struct {
	RangeLabel   string
	Contributors []aggregate.Contributor
	Now          time.Time
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
	contribBarW = 22
)

type palette struct{ on bool }

func (p palette) c(code, s string) string {
	if !p.on || code == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Dashboard prints all four panels in order.
func Dashboard(w io.Writer, m Model, color bool) {
	p := palette{on: color}

	fmt.Fprintln(w)
	p.header(w, "Repo", "")
	fmt.Fprintln(w)
	p.vitals(w, m.Vitals)

	fmt.Fprintln(w)
	p.header(w, "Activity", m.RangeLabel)
	fmt.Fprintln(w)
	p.heatmap(w, m)

	fmt.Fprintln(w)
	p.header(w, "Top contributors", "")
	fmt.Fprintln(w)
	p.contributors(w, m.Contributors)

	fmt.Fprintln(w)
	p.header(w, "Codebase growth", "6mo")
	fmt.Fprintln(w)
	p.growth(w, m.Growth, m.HotFiles)
	fmt.Fprintln(w)
}

// Graph prints a focused activity drill-down: the familiar heatmap, a taller
// bucketed activity chart, and exact bucket totals for scripting-by-eyeball.
func Graph(w io.Writer, m GraphModel, color bool) {
	p := palette{on: color}

	fmt.Fprintln(w)
	p.header(w, "Activity graph", m.RangeLabel+" · "+m.Bucket)
	fmt.Fprintln(w)
	p.heatmap(w, Model{Days: m.Days, TotalCommits: m.TotalCommits, Streak: m.Streak, Now: m.Now})

	if chart := p.activityChart(m.Buckets, activityChartHeight); len(chart) > 0 {
		fmt.Fprintln(w)
		for _, line := range chart {
			fmt.Fprintln(w, "  "+line)
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "  "+p.c(cLabel, "counts"))
	if len(m.Buckets) == 0 || m.TotalCommits == 0 {
		fmt.Fprintln(w, "    "+p.c(cLabel, "no commits in range"))
		fmt.Fprintln(w)
		return
	}
	countW := 0
	for _, b := range m.Buckets {
		if n := len(strconv.Itoa(b.Count)); n > countW {
			countW = n
		}
	}
	for _, b := range m.Buckets {
		count := p.c(cLabel, fmt.Sprintf("%*d", countW, b.Count))
		fmt.Fprintf(w, "    %s   %s\n", count, periodLabel(b, m.Bucket))
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
	p.contributors(w, m.Contributors)

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
	// Files arrive sorted by descending commit count, so the first is the peak.
	maxC := m.Files[0].Commits
	for _, f := range m.Files {
		filled := 0
		if maxC > 0 {
			filled = int(float64(f.Commits)/float64(maxC)*float64(contribBarW) + 0.5)
		}
		if filled < 1 {
			filled = 1 // always show a sliver so every file reads as present
		}
		if filled > contribBarW {
			filled = contribBarW
		}
		bar := p.c(cAccent, strings.Repeat(barFill, filled)) +
			strings.Repeat(" ", contribBarW-filled)
		count := p.c(cLabel, fmt.Sprintf("%*d", countW, f.Commits))
		fmt.Fprintf(w, "  %s   %s   %s\n", bar, count, f.Path)
	}

	fmt.Fprintln(w)
	summary := fmt.Sprintf("%d %s touched", len(m.Files), plural(len(m.Files), "file", "files"))
	fmt.Fprintln(w, "  "+p.c(cLabel, summary))
	fmt.Fprintln(w)
}

func (p palette) header(w io.Writer, label, suffix string) {
	s := strings.ToUpper(label)
	if suffix != "" {
		s += "  ·  " + suffix
	}
	fmt.Fprintln(w, p.c(cLabel, s))
}

func (p palette) vitals(w io.Writer, v gitdata.Vitals) {
	dotColor := cAccent
	if v.DirtyFiles > 0 {
		dotColor = cAmber
	}
	parts := []string{p.c(dotColor, "●") + " " + p.c(cBright, v.Branch)}
	if v.HasUpstream {
		parts = append(parts, fmt.Sprintf("%s%d %s%d",
			p.c(cAccent, "↑"), v.Ahead, p.c(cLabel, "↓"), v.Behind))
	}
	parts = append(parts,
		p.c(cLabel, fmt.Sprintf("%d dirty", v.DirtyFiles)),
		p.c(cLabel, fmt.Sprintf("%d %s", v.StashCount, plural(v.StashCount, "stash", "stashes"))),
		p.c(cLabel, fmt.Sprintf("%d %s", v.BranchCount, plural(v.BranchCount, "branch", "branches"))),
	)
	fmt.Fprintln(w, "  "+strings.Join(parts, "   "))
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
	for r := 0; r < 7; r++ {
		var b strings.Builder
		b.WriteString("  ")
		for col := 0; col < cols; col++ {
			b.WriteString(p.cellGlyph(grid[r][col], max))
			b.WriteByte(' ')
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
	fmt.Fprintln(w)
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

func (p palette) contributors(w io.Writer, cs []aggregate.Contributor) {
	if len(cs) == 0 {
		fmt.Fprintln(w, "  "+p.c(cLabel, "no commits in range"))
		return
	}
	nameW, countW := 0, 0
	for _, c := range cs {
		if n := runeLen(c.Name); n > nameW {
			nameW = n
		}
		if n := len(strconv.Itoa(c.Commits)); n > countW {
			countW = n
		}
	}
	if nameW > 16 {
		nameW = 16
	}
	maxC := cs[0].Commits
	for _, c := range cs {
		name := truncate(c.Name, nameW)
		filled := 0
		if maxC > 0 {
			filled = int(float64(c.Commits)/float64(maxC)*float64(contribBarW) + 0.5)
		}
		if filled < 1 {
			filled = 1 // always show a sliver so every contributor reads as present
		}
		if filled > contribBarW {
			filled = contribBarW
		}
		// Fill-only bar: a green run padded with spaces (no track). Compact
		// enough to keep rows tight, but with no dim block to stack up; the
		// space padding still lines the counts up.
		bar := p.c(cAccent, strings.Repeat(barFill, filled)) +
			strings.Repeat(" ", contribBarW-filled)
		pad := strings.Repeat(" ", nameW-runeLen(name))
		count := p.c(cLabel, fmt.Sprintf("%*d", countW, c.Commits))
		fmt.Fprintf(w, "  %s%s   %s   %s\n", name, pad, bar, count)
	}
}

func (p palette) growth(w io.Writer, g aggregate.Growth, hot []aggregate.FileChurn) {
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
		fmt.Fprintln(w)
		for _, line := range chart {
			fmt.Fprintln(w, "  "+line)
		}
	}

	if len(hot) > 0 {
		countW := 0
		for _, f := range hot {
			if n := len(strconv.Itoa(f.Commits)); n > countW {
				countW = n
			}
		}
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  "+p.c(cLabel, "hot files"))
		for _, f := range hot {
			// Count-first keeps the numbers aligned and the (long) paths from
			// wrapping mid-string the way an inline list does.
			count := p.c(cLabel, fmt.Sprintf("%*d", countW, f.Commits))
			fmt.Fprintf(w, "    %s   %s\n", count, f.Path)
		}
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
	lines := make([]string, height)
	for r := 0; r < height; r++ {
		cellBottom := (height - 1 - r) * 8
		var b strings.Builder
		for _, v := range vals {
			eighths := int(float64(v)/float64(max)*float64(height*8) + 0.5)
			fill := eighths - cellBottom
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

func runeLen(s string) int { return len([]rune(s)) }

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}
