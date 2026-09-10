package render

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/lcondliffe/gitling/internal/forge"
	"github.com/lcondliffe/gitling/internal/gitdata"
)

func TestGoldenCompact(t *testing.T) {
	for _, width := range []int{120, 40} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := goldenWideCharModel()
			m.Width, m.Height, m.Layout = width, 18, "compact"
			m.Vitals.Branch = strings.Repeat("long-branch-", 8)
			m.Vitals.Conflicts = 2
			m.Vitals.Operation = gitdata.Operation{Kind: gitdata.OpRebase, Step: 3, Total: 7}
			var b bytes.Buffer
			Dashboard(&b, m, false)
			out := b.String()
			for _, want := range []string{"rebase 3/7", "2 conflicts", "omitted", "staged", "↓1"} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q:\n%s", want, out)
				}
			}
			if strings.Count(out, "\n") > m.Height {
				t.Errorf("exceeds height:\n%s", out)
			}
			for _, line := range strings.Split(out, "\n") {
				if visibleLen(line) > width {
					t.Errorf("overwide: %q", line)
				}
			}
			checkGolden(t, fmt.Sprintf("compact-%d.golden.txt", width), b.Bytes())
		})
	}
}

func TestCompactPriorityAndFullLayouts(t *testing.T) {
	m := goldenModel()
	m.Width, m.Height = 80, 3
	m.Vitals.Operation = gitdata.Operation{Kind: gitdata.OpMerge}
	var b bytes.Buffer
	Dashboard(&b, m, false)
	if !strings.Contains(b.String(), "merge in progress") || !strings.Contains(b.String(), "exceeds") {
		t.Fatal(b.String())
	}
	for _, layout := range []string{"wide", "stack"} {
		m.Layout = layout
		b.Reset()
		Dashboard(&b, m, false)
		m.Height = 0
		var full bytes.Buffer
		Dashboard(&full, m, false)
		if b.String() != full.String() {
			t.Errorf("%s changed with height", layout)
		}
		m.Height = 3
	}
	if !ValidLayout("compact") {
		t.Fatal("compact must be valid")
	}
}

func TestTerminalHeightPipeStable(t *testing.T) {
	t.Setenv("LINES", "12")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if n, ok := TerminalHeight(w); ok || n != 0 {
		t.Fatalf("pipe height = %d, %v", n, ok)
	}
}

func TestCompactOpenWorkBeforeHistory(t *testing.T) {
	m := goldenModel()
	m.Width, m.Height, m.Layout = 80, 12, LayoutCompact
	m.PRs = []forge.PR{{Number: 42, Title: "resolve release blocker", Author: "Ada", Updated: goldenNow}}
	var b bytes.Buffer
	Dashboard(&b, m, true)
	out := b.String()
	if !strings.Contains(out, "#42") || !strings.Contains(out, "omitted: ACTIVITY") {
		t.Fatal(out)
	}
	if strings.Contains(out, "2440dfe") {
		t.Fatal("history displaced open work")
	}
}
