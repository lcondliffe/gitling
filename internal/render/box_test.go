package render

import (
	"strings"
	"testing"
)

func TestVisibleLenIgnoresANSI(t *testing.T) {
	p := palette{on: true}
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"plain", "hello", 5},
		{"empty", "", 0},
		{"colored", p.c(cAccent, "hello"), 5},
		{"partly colored", "a" + p.c(cAccent, "bc") + "d", 4},
		{"multibyte", "■ ■ □", 5},
		{"colored multibyte", p.c(cAccent, "■■■"), 3},
		{"unterminated escape", "ab\x1b[38;5;40", 2},
	}
	for _, tc := range cases {
		if got := visibleLen(tc.in); got != tc.want {
			t.Errorf("%s: visibleLen(%q) = %d, want %d", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestClipVisibleKeepsColorAndWidth(t *testing.T) {
	p := palette{on: true}

	if got := clipVisible("hello", 10); got != "hello" {
		t.Errorf("short string should pass through unchanged, got %q", got)
	}
	if got := clipVisible("hello", 0); got != "" {
		t.Errorf("clip to 0 = %q, want empty", got)
	}

	got := clipVisible("hello world", 5)
	if visibleLen(got) != 5 {
		t.Errorf("clipped %q to visible width %d, want 5", got, visibleLen(got))
	}
	if !strings.HasSuffix(got, ellipsis) {
		t.Errorf("clipped string should end in an ellipsis, got %q", got)
	}

	// A clipped colored string must re-arm the reset so color can't bleed into
	// the box border that follows it.
	colored := clipVisible(p.c(cAccent, "hello world"), 5)
	if visibleLen(colored) != 5 {
		t.Errorf("clipped colored string visible width = %d, want 5", visibleLen(colored))
	}
	if !strings.HasSuffix(colored, "\x1b[0m") {
		t.Errorf("clipped colored string should end with a reset, got %q", colored)
	}
}

func TestPadVisiblePadsToWidth(t *testing.T) {
	p := palette{on: true}
	if got := padVisible(p.c(cAccent, "ab"), 5); visibleLen(got) != 5 {
		t.Errorf("padded colored string to %d, want 5", visibleLen(got))
	}
	if got := padVisible("abcdef", 3); got != "abcdef" {
		t.Errorf("padVisible must not truncate, got %q", got)
	}
}

func TestBoxFramesContentAtExactWidth(t *testing.T) {
	p := palette{}
	lines := p.box("TITLE", []string{"  short", "  a much longer line than fits"}, 20)

	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4 (top, 2 content, bottom): %q", len(lines), lines)
	}
	for i, line := range lines {
		if got := visibleLen(line); got != 22 { // 20 inner + 2 borders
			t.Errorf("line %d %q has width %d, want 22", i, line, got)
		}
	}
	if !strings.HasPrefix(lines[0], boxTL+boxH+" TITLE ") {
		t.Errorf("top edge should inlay the title, got %q", lines[0])
	}
	if !strings.HasSuffix(lines[0], boxTR) || !strings.HasSuffix(lines[3], boxBR) {
		t.Errorf("corners missing: %q / %q", lines[0], lines[3])
	}
	if !strings.Contains(lines[2], ellipsis) {
		t.Errorf("overlong content should be clipped with an ellipsis, got %q", lines[2])
	}
}

func TestBoxDropsTitleWhenTooNarrow(t *testing.T) {
	p := palette{}
	lines := p.box("A VERY LONG TITLE", []string{"x"}, 3)
	if strings.Contains(lines[0], "VERY") {
		t.Errorf("title should be dropped or truncated in a narrow box, got %q", lines[0])
	}
	for _, line := range lines {
		if got := visibleLen(line); got != 5 {
			t.Errorf("narrow box line %q has width %d, want 5", line, got)
		}
	}
}

func TestBoxSurvivesDegenerateWidth(t *testing.T) {
	p := palette{}
	for _, innerW := range []int{-5, 0, 1} {
		lines := p.box("TITLE", []string{"  content"}, innerW)
		if len(lines) != 3 {
			t.Fatalf("innerW %d: got %d lines, want 3", innerW, len(lines))
		}
		want := visibleLen(lines[0])
		for _, line := range lines {
			if got := visibleLen(line); got != want {
				t.Errorf("innerW %d: ragged box, %q is %d wide, want %d", innerW, line, got, want)
			}
		}
	}
}

func TestSideBySideAlignsUnevenColumns(t *testing.T) {
	left := []string{"aaa", "aaa", "aaa"}
	right := []string{"bbb"}

	got := sideBySide(left, right, 3, 1)
	if len(got) != 3 {
		t.Fatalf("got %d lines, want 3 (the taller column)", len(got))
	}
	if got[0] != "aaa bbb" {
		t.Errorf("first line = %q, want %q", got[0], "aaa bbb")
	}
	// The short column simply ends; trailing blanks are trimmed.
	if got[1] != "aaa" {
		t.Errorf("second line = %q, want %q (right column exhausted)", got[1], "aaa")
	}

	// A left column shorter than leftW is padded so the right column stays put.
	got = sideBySide([]string{"a"}, []string{"b"}, 5, 2)
	if got[0] != "a"+strings.Repeat(" ", 4+2)+"b" {
		t.Errorf("padding to leftW failed: %q", got[0])
	}
}

func TestContentWidthMeasuresWidestVisibleLine(t *testing.T) {
	p := palette{on: true}
	got := contentWidth([]string{"ab", p.c(cAccent, "abcd")}, []string{"abc"})
	if got != 4 {
		t.Errorf("contentWidth = %d, want 4", got)
	}
	if got := contentWidth(); got != 0 {
		t.Errorf("contentWidth() with no blocks = %d, want 0", got)
	}
}

func TestPackPartsWrapsToWidth(t *testing.T) {
	parts := []string{"aaa", "bbb", "ccc"}

	if got := packParts(parts, 0, 3); len(got) != 1 || got[0] != "aaa   bbb   ccc" {
		t.Errorf("unbounded width should keep one line, got %q", got)
	}
	got := packParts(parts, 9, 3)
	if len(got) != 2 || got[0] != "aaa   bbb" || got[1] != "ccc" {
		t.Errorf("packParts(width 9) = %q, want [\"aaa   bbb\" \"ccc\"]", got)
	}
	// A part wider than the line still gets its own line rather than vanishing.
	got = packParts([]string{"aaaaaaaaaa", "b"}, 3, 3)
	if len(got) != 2 || got[0] != "aaaaaaaaaa" || got[1] != "b" {
		t.Errorf("oversized part = %q, want each on its own line", got)
	}
}

// FuzzClipVisible guards the invariant the box borders rest on: whatever is
// thrown at it, the result never exceeds the requested width and never panics.
// Panel content includes commit subjects and author names, which are arbitrary
// bytes and may carry a stray or truncated ESC.
func FuzzClipVisible(f *testing.F) {
	f.Add("hello", 3)
	f.Add("\x1b[38;5;40mhi\x1b[0m", 2)
	f.Add("aaaa\x1b[38;5;40", 5) // unterminated escape at the end
	f.Add("\x1b", 1)
	f.Add("■ ■ □", 2)
	f.Fuzz(func(t *testing.T, s string, max int) {
		got := clipVisible(s, max)
		if max > 0 && visibleLen(got) > max {
			t.Fatalf("clipVisible(%q, %d) = %q: visible width %d exceeds max", s, max, got, visibleLen(got))
		}
		padVisible(got, max)
	})
}
