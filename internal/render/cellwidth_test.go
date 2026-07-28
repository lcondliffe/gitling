package render

import (
	"strings"
	"testing"
)

func TestRuneWidth(t *testing.T) {
	cases := []struct {
		name string
		r    rune
		want int
	}{
		{"ascii letter", 'a', 1},
		{"ascii space", ' ', 1},
		{"latin-1 accented", 'é', 1},
		{"middle dot (ambiguous, heatmap glyph)", '·', 1},
		{"box drawing", '─', 1},
		{"block element", '█', 1},
		{"sparkline block", '▁', 1},
		{"filled square (heatmap)", '■', 1},
		{"hollow square (today marker)", '□', 1},
		{"ellipsis", '…', 1},
		{"kanji", '日', 2},
		{"hiragana", 'あ', 2},
		{"hangul syllable", '김', 2},
		{"fullwidth latin", 'Ａ', 2},
		{"ideographic space", '　', 2},
		{"emoji", '🎉', 2},
		{"emoji bug", '🐛', 2},
		{"combining acute", '́', 0},
		{"zero-width joiner", '‍', 0},
		{"variation selector 16", '️', 0},
		{"skin tone modifier", '\U0001F3FB', 0},
		{"control char", '\n', 0},
	}
	for _, tc := range cases {
		if got := runeWidth(tc.r); got != tc.want {
			t.Errorf("%s: runeWidth(%U) = %d, want %d", tc.name, tc.r, got, tc.want)
		}
	}
}

func TestCellLen(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"山田太郎", 8},
		{"feat: 日本語 🎉", 6 + 6 + 1 + 2},
		{"é", 1},                   // e + combining acute renders as one cell
		{"\U0001F44D\U0001F3FB", 2}, // thumbs up + skin tone is one glyph
	}
	for _, tc := range cases {
		if got := cellLen(tc.in); got != tc.want {
			t.Errorf("cellLen(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// The width tables are binary-searched, so a table that is unsorted or has
// overlapping intervals would silently return wrong widths for some runes.
func TestWidthTablesSortedAndDisjoint(t *testing.T) {
	for _, tbl := range []struct {
		name  string
		table [][2]rune
	}{{"zeroWidth", zeroWidth}, {"wide", wide}} {
		for i, r := range tbl.table {
			if r[0] > r[1] {
				t.Errorf("%s[%d] = %U-%U: range is inverted", tbl.name, i, r[0], r[1])
			}
			if i > 0 && r[0] <= tbl.table[i-1][1] {
				t.Errorf("%s[%d] = %U-%U overlaps or is out of order with previous %U-%U",
					tbl.name, i, r[0], r[1], tbl.table[i-1][0], tbl.table[i-1][1])
			}
		}
	}
}

// A double-width rune can't be split across a cut, so clipVisible and truncate
// drop it whole — the result may be a cell short of the limit, but must never
// exceed it, which is what would push a box border out.
func TestClipVisibleWideRunes(t *testing.T) {
	cases := []struct {
		in  string
		max int
	}{
		{"日本語のコミット", 10},
		{"日本語のコミット", 5},
		{"日本語のコミット", 4}, // cut lands mid-rune
		{"a日b日c日", 6},
		{"🎉🎉🎉🎉", 5},
		{"山田太郎 Yamada", 8},
	}
	for _, tc := range cases {
		got := clipVisible(tc.in, tc.max)
		if w := visibleLen(got); w > tc.max {
			t.Errorf("clipVisible(%q, %d) = %q: width %d exceeds max", tc.in, tc.max, got, w)
		}
	}
}

func TestTruncateWideRunes(t *testing.T) {
	cases := []struct {
		in  string
		max int
	}{
		{"日本語のコミット", 10},
		{"日本語のコミット", 7},
		{"日本語のコミット", 4},
		{"mixed 中文 text", 9},
	}
	for _, tc := range cases {
		got := truncate(tc.in, tc.max)
		if w := cellLen(got); w > tc.max {
			t.Errorf("truncate(%q, %d) = %q: width %d exceeds max", tc.in, tc.max, got, w)
		}
	}
}

func TestElidePathWideRunes(t *testing.T) {
	cases := []struct {
		path   string
		maxLen int
	}{
		{"docs/日本語/設計とアーキテクチャ.md", 20},
		{"docs/日本語/設計とアーキテクチャ.md", 12},
		{"docs/日本語/設計とアーキテクチャ.md", 6},
		{"内部/レンダー/描画.go", 10},
	}
	for _, tc := range cases {
		got := elidePath(tc.path, tc.maxLen)
		if w := cellLen(got); w > tc.maxLen {
			t.Errorf("elidePath(%q, %d) = %q: width %d exceeds max", tc.path, tc.maxLen, got, w)
		}
	}
}

// No row overflows its terminal width with double-width content in every
// panel, across a range of widths.
//
// This measures with visibleLen, the same function the renderer sizes with, so
// it is self-consistent by construction and cannot catch a regression in the
// width tables themselves — a uniform "everything is one cell" bug passes here.
// TestGoldenDashboardWideChars is the guard for that, since it compares bytes
// against a reviewed fixture. What this catches is asymmetry the goldens don't
// pin at other widths: a clip or pad that loses a cell only at some sizes.
func TestDashboardWideCharsRowWidths(t *testing.T) {
	for _, width := range []int{70, 100, 120, 160} {
		m := goldenWideCharModel()
		m.Width = width
		var buf strings.Builder
		Dashboard(&buf, m, false)

		for i, line := range strings.Split(buf.String(), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			// sideBySide trims trailing blanks, so rows where the right column
			// has ended are legitimately short; no row may ever exceed width.
			if w := visibleLen(line); w > width {
				t.Errorf("width %d: row %d measures %d cells, exceeds terminal width:\n%s",
					width, i, w, line)
			}
		}
	}
}

// Colored output must measure the same as plain: the escape sequences are
// skipped, the wide runes inside them are not.
func TestVisibleLenWideRunesWithColor(t *testing.T) {
	p := palette{on: true}
	plain := "山田太郎 🎉"
	colored := p.c(cAccent, plain)
	if got, want := visibleLen(colored), cellLen(plain); got != want {
		t.Errorf("visibleLen(colored %q) = %d, want %d", plain, got, want)
	}
}
