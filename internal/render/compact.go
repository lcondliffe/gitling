package render

import (
	"fmt"
	"strings"
)

const compactRows = 24

// Compact uses full-width panels: state has an unlimited minimum height; every
// optional panel is atomic (two borders plus its entire body). A wrapped footer
// always names omitted panels. Never trim state just to satisfy the row budget.
func (p palette) dashboardCompact(m Model) []string {
	width := m.Width
	if width <= 0 {
		width = DefaultWidth
	}
	inner := max(1, width-2)
	budget := compactRows
	if m.Height > 0 {
		budget = min(budget, m.Height)
	}
	state := wrapText(strings.Join((palette{}).vitalsLines(m, inner), "\n"), inner)
	code := cLabel
	if m.Vitals.Operation.InProgress() || m.Vitals.Conflicts > 0 {
		code = cRed
	}
	for i := range state {
		state[i] = p.c(code, state[i])
	}
	out := p.box("REPO · compact", state, inner)
	type panel struct {
		name  string
		lines []string
	}
	panels := []panel{}
	// Open work is ahead of historical activity. No PR row is silently dropped.
	if len(m.PRs) > 0 {
		panels = append(panels, panel{prTitle(m), p.prLines(m, inner)})
	}
	activity := fmt.Sprintf("%d commits requested · streak %dd; chart collapsed", m.TotalCommits, m.Streak)
	if len(m.Days) > 0 {
		activity += "\n" + dateInterval(m.Days[0].Date, m.Days[len(m.Days)-1].Date) + "; " + historyScope(m.DateBasis)
	}
	panels = append(panels, panel{"ACTIVITY", p.textLines(activity, inner)})
	if len(m.Recent) > 0 {
		panels = append(panels, panel{recentTitle(m), p.recentLines(m, inner)})
	}
	panels = append(panels, panel{"CONTRIBUTORS", p.contributorLines(m, inner)}, panel{"NET-LINE HISTORY", p.growthLines(m, inner)})
	if len(m.HotFiles) > 0 {
		panels = append(panels, panel{"HOT FILES", p.hotFileLines(m, inner)})
	}
	footer := func(start int) []string {
		if start == len(panels) {
			return nil
		}
		names := make([]string, 0, len(panels)-start)
		for _, panel := range panels[start:] {
			names = append(names, panel.name)
		}
		return p.textLines("omitted: "+strings.Join(names, ", ")+"; --layout stack for full details", width)
	}
	for i, panel := range panels {
		box := p.box(panel.name, panel.lines, inner)
		if len(out)+len(box)+len(footer(i+1))+2 > budget {
			out = append(out, footer(i)...)
			if len(out)+2 > budget {
				out = append(out, p.textLines("state exceeds height; scroll for details", width)...)
			}
			return out
		}
		out = append(out, box...)
	}
	return out
}

// wrapText is lossless for plain text, including long unbroken branch names.
// Labels wrap instead of being clipped by box(), which could hide their scope.
func wrapText(text string, width int) []string {
	if width <= 0 {
		return strings.Split(text, "\n")
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		for cellLen(line) > width {
			prefix := head(line, width)
			if prefix == "" { // a single wide glyph in a one-cell terminal: keep it
				_, n := stepCell(line, 0)
				prefix = line[:n]
			}
			cut := len(prefix)
			if space := strings.LastIndexByte(prefix, ' '); space > 0 {
				cut = space
			}
			out = append(out, line[:cut])
			line = strings.TrimLeft(line[cut:], " ")
		}
		out = append(out, line)
	}
	return out
}

func (p palette) textLines(text string, width int) []string {
	lines := wrapText(text, width)
	for i := range lines {
		lines[i] = p.c(cLabel, lines[i])
	}
	return lines
}
