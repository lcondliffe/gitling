package render

import (
	"strings"
	"unicode/utf8"
)

// Box drawing for the dashboard grid.
//
// Every helper here measures *visible* width — the number of printable cells a
// string occupies once ANSI SGR sequences are discarded. Panel content arrives
// already colored, so plain len() (bytes) and even runeLen() (runes, counting
// escape characters) would both overshoot and blow the right border out.
//
// Width is counted in runes, matching the rest of the package: gitling's glyph
// set (box drawing, block elements, ■/□/·) is single-width in practice, so no
// East Asian width table is carried around for it.

const (
	boxTL, boxTR, boxBL, boxBR = "╭", "╮", "╰", "╯"
	boxH, boxV                 = "─", "│"
	ellipsis                   = "…"
)

// visibleLen returns the printable width of s, skipping ANSI escape sequences.
func visibleLen(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i = skipEscape(s, i)
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		n++
	}
	return n
}

// skipEscape returns the index just past the escape sequence starting at i.
// Only the SGR sequences this package emits ("\x1b[...m") are expected; an
// unterminated sequence swallows the rest of the string rather than looping.
func skipEscape(s string, i int) int {
	j := i + 1
	for j < len(s) && s[j] != 'm' {
		j++
	}
	return j + 1
}

// clipVisible truncates s to at most max visible columns, preserving the escape
// sequences it passes over and re-arming the reset so color can't leak past the
// cut. It is a safety net for the box borders: panels are told the width they
// have and normally fit on their own.
func clipVisible(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if visibleLen(s) <= max {
		return s
	}
	var b strings.Builder
	seen, colored := 0, false
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := skipEscape(s, i)
			b.WriteString(s[i:j])
			colored = true
			i = j
			continue
		}
		if seen == max-1 {
			break // leave room for the ellipsis
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		i += size
		seen++
	}
	b.WriteString(ellipsis)
	if colored {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// padVisible right-pads s with spaces to exactly w visible columns.
func padVisible(s string, w int) string {
	if pad := w - visibleLen(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// box frames content in a rounded border innerW columns wide (excluding the
// border itself), with title inlaid in the top edge:
//
//	╭─ TITLE ─────────╮
//	│   content       │
//	╰─────────────────╯
//
// Content lines carry their own leading indent (every panel renderer indents by
// two), so nothing is added on the left beyond the border.
func (p palette) box(title string, content []string, innerW int) []string {
	if innerW < 1 {
		innerW = 1
	}
	lines := make([]string, 0, len(content)+2)
	lines = append(lines, p.c(cLabel, boxTop(title, innerW)))
	for _, line := range content {
		lines = append(lines, p.c(cLabel, boxV)+padVisible(clipVisible(line, innerW), innerW)+p.c(cLabel, boxV))
	}
	lines = append(lines, p.c(cLabel, boxBL+strings.Repeat(boxH, innerW)+boxBR))
	return lines
}

// boxTop builds the titled top edge. Callers pass a title already cased by
// titleWith; it is dropped entirely when the box is too narrow to hold it.
func boxTop(title string, innerW int) string {
	title = strings.TrimSpace(title)
	// "╭" + "─ " + title + " " + fill + "╮": the fixed parts cost 3 of innerW.
	fill := innerW - 3 - runeLen(title)
	if title == "" || fill < 0 {
		if title != "" && innerW > 4 {
			title = truncate(title, innerW-3)
			fill = innerW - 3 - runeLen(title)
		} else {
			return boxTL + strings.Repeat(boxH, innerW) + boxTR
		}
	}
	return boxTL + boxH + " " + title + " " + strings.Repeat(boxH, fill) + boxTR
}

// stackBoxes concatenates boxes vertically, flush against each other: adjacent
// borders read as a divider, and the dashboard stays compact.
func stackBoxes(boxes ...[]string) []string {
	var out []string
	for _, b := range boxes {
		out = append(out, b...)
	}
	return out
}

// sideBySide places two rendered columns next to each other, separated by
// gutter spaces. Columns of unequal height are bottom-filled with blanks, so
// the shorter one simply ends early — matching how a two-column layout flows.
// leftW is the visible width the left column occupies.
func sideBySide(left, right []string, leftW, gutter int) []string {
	n := max(len(left), len(right))
	out := make([]string, 0, n)
	gap := strings.Repeat(" ", max(gutter, 0))
	for i := range n {
		var l, r string
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		line := padVisible(l, leftW) + gap + r
		out = append(out, strings.TrimRight(line, " "))
	}
	return out
}

// contentWidth is the widest visible line across the given blocks, used to size
// a box to its content when the terminal width is unknown.
func contentWidth(blocks ...[]string) int {
	w := 0
	for _, block := range blocks {
		for _, line := range block {
			if n := visibleLen(line); n > w {
				w = n
			}
		}
	}
	return w
}
