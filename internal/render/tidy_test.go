package render

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lcondliffe/gitling/internal/gitdata"
	"github.com/lcondliffe/gitling/internal/tidy"
)

func goldenTidyPlan() tidy.Plan {
	br := func(name, tip string, ago time.Duration) gitdata.Branch {
		return gitdata.Branch{Name: name, Tip: tip, LastCommit: goldenNow.Add(-ago)}
	}
	return tidy.Plan{
		Base:       "origin/main",
		Total:      9,
		StaleAfter: tidy.DefaultStaleAfter,
		Candidates: []tidy.Candidate{
			{Branch: br("chore/tidy-readme", "a1b2c3d4e5f6", 14*24*time.Hour), Reason: tidy.ReasonMerged},
			{Branch: br("feat/heatmap", "9f8e7d6c5b4a", 35*24*time.Hour), Reason: tidy.ReasonGone, Force: true},
			{Branch: br("fix/parse-numstat", "4c5b6a7d8e9f", 100*24*time.Hour), Reason: tidy.ReasonGone, Force: true},
			{Branch: br("spike/old-idea", "1122334455aa", 300*24*time.Hour), Reason: tidy.ReasonStale, Force: true},
		},
		Protected: []tidy.Protected{
			{Branch: br("release/1.x", "ffee00112233", 200*24*time.Hour), Why: "protected by release/*"},
		},
	}
}

func TestGoldenTidyDryRun(t *testing.T) {
	var buf bytes.Buffer
	Tidy(&buf, TidyModel{Plan: goldenTidyPlan(), Now: goldenNow, Width: 90}, false)
	checkGolden(t, "tidy-dry-run.golden.txt", buf.Bytes())
}

func TestGoldenTidyApplied(t *testing.T) {
	var buf bytes.Buffer
	Tidy(&buf, TidyModel{
		Plan:    goldenTidyPlan(),
		Now:     goldenNow,
		Width:   90,
		Applied: true,
		Failed:  map[string]error{"feat/heatmap": errors.New("git branch -D: the branch is not fully merged\nsecond line")},
	}, false)
	checkGolden(t, "tidy-applied.golden.txt", buf.Bytes())
}

func TestTidyEmptyPlan(t *testing.T) {
	var buf bytes.Buffer
	Tidy(&buf, TidyModel{Plan: tidy.Plan{Total: 3, Base: "main"}, Now: goldenNow}, false)
	if got := buf.String(); !strings.Contains(got, "nothing to clean up") {
		t.Errorf("empty plan output:\n%s", got)
	}
}

// The dry-run footer must not survive into the confirmation prompt or the
// applied report.
func TestTidyDryRunFooterOnlyOnDryRuns(t *testing.T) {
	plan := goldenTidyPlan()
	cases := []struct {
		name string
		m    TidyModel
		want bool
	}{
		{"dry run", TidyModel{Plan: plan, Now: goldenNow}, true},
		{"confirming", TidyModel{Plan: plan, Now: goldenNow, Confirming: true}, false},
		{"applied", TidyModel{Plan: plan, Now: goldenNow, Applied: true}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			Tidy(&buf, c.m, false)
			if got := strings.Contains(buf.String(), "nothing has been deleted"); got != c.want {
				t.Errorf("contains the dry-run footer = %v, want %v:\n%s", got, c.want, buf.String())
			}
		})
	}
}

// Every row carries the hash it pointed at, and the applied report says what to
// do with them — that pairing is the whole undo story.
func TestTidyAppliedShowsRestoreHint(t *testing.T) {
	var buf bytes.Buffer
	Tidy(&buf, TidyModel{Plan: goldenTidyPlan(), Now: goldenNow, Applied: true}, false)
	got := buf.String()
	if !strings.Contains(got, "git branch <name> <hash>") {
		t.Errorf("missing restore hint:\n%s", got)
	}
	if !strings.Contains(got, "a1b2c3d") {
		t.Errorf("missing short hash:\n%s", got)
	}
}

// Nothing was deleted, so there is nothing to restore and no hint to give.
func TestTidyAppliedWithAllFailuresOmitsRestoreHint(t *testing.T) {
	plan := tidy.Plan{
		Base:  "main",
		Total: 1,
		Candidates: []tidy.Candidate{
			{Branch: gitdata.Branch{Name: "feat/x", Tip: "abc1234", LastCommit: goldenNow}, Reason: tidy.ReasonGone, Force: true},
		},
	}
	var buf bytes.Buffer
	Tidy(&buf, TidyModel{
		Plan: plan, Now: goldenNow, Applied: true,
		Failed: map[string]error{"feat/x": errors.New("nope")},
	}, false)

	got := buf.String()
	if strings.Contains(got, "git branch <name> <hash>") {
		t.Errorf("restore hint shown with nothing deleted:\n%s", got)
	}
	if !strings.Contains(got, "0 branches deleted") || !strings.Contains(got, "1 kept after errors") {
		t.Errorf("failure summary:\n%s", got)
	}
}

// A multi-line git error must not break the row layout.
func TestTidyKeepsErrorsToOneLine(t *testing.T) {
	plan := goldenTidyPlan()
	var buf bytes.Buffer
	Tidy(&buf, TidyModel{
		Plan: plan, Now: goldenNow, Applied: true,
		Failed: map[string]error{"feat/heatmap": errors.New("first line\nsecond line")},
	}, false)
	if strings.Contains(buf.String(), "second line") {
		t.Errorf("multi-line error leaked into the table:\n%s", buf.String())
	}
}

func TestTidyNeverExceedsTerminalWidth(t *testing.T) {
	plan := goldenTidyPlan()
	plan.Candidates[0].Branch.Name = strings.Repeat("very-long-branch-name/", 6)
	for _, width := range []int{40, 60, 80, 120} {
		var buf bytes.Buffer
		Tidy(&buf, TidyModel{Plan: plan, Now: goldenNow, Width: width}, false)
		for _, line := range strings.Split(buf.String(), "\n") {
			if runeLen(line) > width {
				t.Errorf("width %d: line of %d columns: %q", width, runeLen(line), line)
			}
		}
	}
}

func TestShortHash(t *testing.T) {
	cases := map[string]string{
		"a1b2c3d4e5f6": "a1b2c3d",
		"a1b2c3d":      "a1b2c3d",
		"abc":          "abc",
		"":             "",
	}
	for in, want := range cases {
		if got := shortHash(in); got != want {
			t.Errorf("shortHash(%q) = %q, want %q", in, got, want)
		}
	}
}
