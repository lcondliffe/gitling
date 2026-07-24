package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lcondliffe/gitling/internal/aggregate"
	"github.com/lcondliffe/gitling/internal/gitdata"
)

func TestBarChartEdges(t *testing.T) {
	p := palette{}
	if got := p.barChart(nil, 3); got != nil {
		t.Fatalf("barChart(nil) = %+v, want nil", got)
	}
	if got := p.barChart([]int{1, 2}, 0); got != nil {
		t.Fatalf("barChart height 0 = %+v, want nil", got)
	}
	if got := p.barChart([]int{0, 0}, 3); got != nil {
		t.Fatalf("barChart all zero = %+v, want nil", got)
	}
}

func TestBarChartRendersScaledRows(t *testing.T) {
	p := palette{}
	got := p.barChart([]int{1, 2, 4}, 2)
	if len(got) != 2 {
		t.Fatalf("barChart rows = %d, want 2: %+v", len(got), got)
	}
	if got[0] == "" || got[1] == "" {
		t.Fatalf("barChart produced empty row(s): %+v", got)
	}
	runes := []rune(got[1])
	if runes[len(runes)-1] != '█' {
		t.Fatalf("bottom row should include a full-height max column, got %q", got[1])
	}
}

func TestGraphNoCommitsShowsCompactCountsMessage(t *testing.T) {
	start := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	buckets := []aggregate.PeriodCount{
		{Start: start, End: start, Count: 0},
		{Start: start.AddDate(0, 0, 1), End: start.AddDate(0, 0, 1), Count: 0},
	}
	var buf bytes.Buffer
	Graph(&buf, GraphModel{
		RangeLabel:   "last 2d",
		Bucket:       "day",
		Buckets:      buckets,
		TotalCommits: 0,
		Now:          start.AddDate(0, 0, 1),
	}, false)

	out := buf.String()
	if !strings.Contains(out, "no commits in range") {
		t.Fatalf("Graph empty range output missing compact message:\n%s", out)
	}
	if strings.Contains(out, "2024-06-01") || strings.Contains(out, "2024-06-02") {
		t.Fatalf("Graph empty range should not print zero-count bucket rows:\n%s", out)
	}
}

func TestChurnRanksFilesWithCountsAndSummary(t *testing.T) {
	var buf bytes.Buffer
	Churn(&buf, ChurnModel{
		RangeLabel: "last 1y",
		Files: []aggregate.FileChurn{
			{Path: "cmd/gitling/main.go", Commits: 8},
			{Path: "internal/render/render.go", Commits: 3},
			{Path: "go.mod", Commits: 1},
		},
		Now: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
	}, false)

	out := buf.String()
	for _, want := range []string{"FILE CHURN", "last 1y", "cmd/gitling/main.go", "3 files touched"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Churn output missing %q:\n%s", want, out)
		}
	}
	// Highest-churn file must render before the lowest.
	if strings.Index(out, "cmd/gitling/main.go") > strings.Index(out, "go.mod") {
		t.Fatalf("Churn should list files by descending commit count:\n%s", out)
	}
}

func TestChurnNoCommitsShowsMessage(t *testing.T) {
	var buf bytes.Buffer
	Churn(&buf, ChurnModel{RangeLabel: "last 2d", Files: nil}, false)

	out := buf.String()
	if !strings.Contains(out, "no commits in range") {
		t.Fatalf("Churn empty range output missing message:\n%s", out)
	}
	if strings.Contains(out, "files touched") {
		t.Fatalf("Churn empty range should not print a file-count summary:\n%s", out)
	}
}

func TestChurnSingularFileSummary(t *testing.T) {
	var buf bytes.Buffer
	Churn(&buf, ChurnModel{
		RangeLabel: "last 2d",
		Files:      []aggregate.FileChurn{{Path: "go.mod", Commits: 1}},
	}, false)

	if out := buf.String(); !strings.Contains(out, "1 file touched") {
		t.Fatalf("Churn should use singular 'file' for one result:\n%s", out)
	}
}

func TestContributorsRanksAuthorsWithSummary(t *testing.T) {
	var buf bytes.Buffer
	Contributors(&buf, ContributorsModel{
		RangeLabel: "last 1y",
		Contributors: []aggregate.Contributor{
			{Name: "Ada Lovelace", Email: "ada@example.com", Commits: 8},
			{Name: "Alan Turing", Email: "alan@example.com", Commits: 3},
		},
		Now: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
	}, false)

	out := buf.String()
	for _, want := range []string{"CONTRIBUTORS", "last 1y", "Ada Lovelace", "Alan Turing", "2 contributors · 11 commits"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Contributors output missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "Ada Lovelace") > strings.Index(out, "Alan Turing") {
		t.Fatalf("Contributors should list authors by descending commit count:\n%s", out)
	}
}

func TestContributorsSingularSummary(t *testing.T) {
	var buf bytes.Buffer
	Contributors(&buf, ContributorsModel{
		RangeLabel:   "last 1y",
		Contributors: []aggregate.Contributor{{Name: "Ada", Email: "ada@example.com", Commits: 1}},
	}, false)

	if out := buf.String(); !strings.Contains(out, "1 contributor · 1 commit") {
		t.Fatalf("Contributors should use singular nouns for a lone author with one commit:\n%s", out)
	}
}

func TestContributorsNoCommitsShowsMessage(t *testing.T) {
	var buf bytes.Buffer
	Contributors(&buf, ContributorsModel{RangeLabel: "last 2d", Contributors: nil}, false)

	out := buf.String()
	if !strings.Contains(out, "no commits in range") {
		t.Fatalf("Contributors empty range output missing message:\n%s", out)
	}
	if strings.Contains(out, "contributor") {
		t.Fatalf("Contributors empty range should not print a summary line:\n%s", out)
	}
}

func TestJSONIncludesDashboardData(t *testing.T) {
	start := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	model := Model{
		Vitals: gitdata.Vitals{
			Branch:      "main",
			HasUpstream: true,
			Ahead:       1,
			Behind:      2,
			DirtyFiles:  3,
			StashCount:  4,
			BranchCount: 5,
		},
		RangeLabel:   "last 2d",
		Days:         []aggregate.DayCount{{Date: start, Count: 2}},
		TotalCommits: 2,
		Streak:       1,
		Contributors: []aggregate.Contributor{{Name: "Ada", Email: "ada@example.com", Commits: 2}},
		Growth: aggregate.Growth{
			TotalLOC: 42,
			Pct:      12.5,
			HasPct:   true,
			Spark:    []int{40, 42},
		},
		HotFiles: []aggregate.FileChurn{{Path: "main.go", Commits: 2}},
		Now:      start,
	}
	buckets := []aggregate.PeriodCount{{Start: start, End: start, Count: 2}}

	var buf bytes.Buffer
	if err := JSON(&buf, model, "day", buckets); err != nil {
		t.Fatalf("JSON returned error: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if got["range"] != "last 2d" {
		t.Fatalf("range = %v, want last 2d", got["range"])
	}
	activity := got["activity"].(map[string]any)
	if activity["total_commits"] != float64(2) || activity["streak_days"] != float64(1) {
		t.Fatalf("activity totals = %#v", activity)
	}
	if activity["bucket"] != "day" {
		t.Fatalf("activity bucket = %#v", activity["bucket"])
	}
	days := activity["days"].([]any)
	if days[0].(map[string]any)["date"] != "2024-06-01" {
		t.Fatalf("day date = %#v", days[0])
	}
	bucketsJSON := activity["buckets"].([]any)
	if bucketsJSON[0].(map[string]any)["commits"] != float64(2) {
		t.Fatalf("bucket commits = %#v", bucketsJSON[0])
	}
	vitals := got["vitals"].(map[string]any)
	if vitals["branch"] != "main" || vitals["has_upstream"] != true || vitals["dirty_files"] != float64(3) {
		t.Fatalf("vitals = %#v", vitals)
	}
	contributors := got["contributors"].([]any)
	contributor := contributors[0].(map[string]any)
	if contributor["email"] != "ada@example.com" || contributor["commits"] != float64(2) {
		t.Fatalf("contributor = %#v", contributor)
	}
	if _, ok := contributor["Email"]; ok {
		t.Fatalf("contributor leaked PascalCase key: %#v", contributor)
	}
	growth := got["growth"].(map[string]any)
	if growth["total_loc"] != float64(42) || growth["pct"] != 12.5 {
		t.Fatalf("growth = %#v", growth)
	}
	hotFiles := got["hot_files"].([]any)
	hotFile := hotFiles[0].(map[string]any)
	if hotFile["path"] != "main.go" || hotFile["commits"] != float64(2) {
		t.Fatalf("hot file = %#v", hotFile)
	}
	if _, ok := hotFile["Path"]; ok {
		t.Fatalf("hot file leaked PascalCase key: %#v", hotFile)
	}
}

func TestBranchesRendering(t *testing.T) {
	now := time.Date(2024, 6, 10, 12, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	Branches(&buf, BranchesModel{
		Now: now,
		Branches: []gitdata.Branch{
			{Name: "main", IsHead: true, Upstream: "origin/main", HasCompare: true, CompareRef: "origin/main", LastCommit: now.Add(-2 * time.Hour), LastAuthor: "Ada"},
			{Name: "feature", Upstream: "", HasCompare: true, CompareRef: "main", Ahead: 5, Behind: 1, LastCommit: now.Add(-3 * 24 * time.Hour), LastAuthor: "Alan"},
			{Name: "stale", Gone: true, LastCommit: now.Add(-20 * 24 * time.Hour), LastAuthor: "Grace"},
		},
	}, false)

	out := buf.String()
	for _, want := range []string{
		"BRANCHES", "3 branches",
		"* main",  // head marker
		"↑5 ↓1",   // feature ahead/behind
		"gone",    // stale upstream
		"vs main", // fallback comparison note for the no-upstream branch
		"2h ago", "3d ago", "20d ago",
		"Ada", "Alan", "Grace",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Branches output missing %q:\n%s", want, out)
		}
	}
	// The upstream-tracked branch should not carry a redundant "vs origin/main".
	if strings.Contains(out, "vs origin/main") {
		t.Fatalf("Branches should not spell out the upstream comparison:\n%s", out)
	}
}

func TestBranchesEmpty(t *testing.T) {
	var buf bytes.Buffer
	Branches(&buf, BranchesModel{Now: time.Now()}, false)
	if out := buf.String(); !strings.Contains(out, "no local branches") {
		t.Fatalf("empty Branches output missing message:\n%s", out)
	}
}

func TestHumanAgo(t *testing.T) {
	now := time.Date(2024, 6, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		t    time.Time
		want string
	}{
		{time.Time{}, "—"},
		{now.Add(10 * time.Second), "just now"}, // future clamps to now
		{now.Add(-30 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "5m ago"},
		{now.Add(-3 * time.Hour), "3h ago"},
		{now.Add(-2 * 24 * time.Hour), "2d ago"},
		{now.Add(-45 * 24 * time.Hour), "1mo ago"},
		{now.Add(-800 * 24 * time.Hour), "2y ago"},
	}
	for _, c := range cases {
		if got := humanAgo(c.t, now); got != c.want {
			t.Errorf("humanAgo(%v) = %q, want %q", c.t, got, c.want)
		}
	}
}

// --- Width-responsive rendering (AGT-290) ---

func TestElidePathUnboundedWhenMaxLenNotPositive(t *testing.T) {
	long := "terraform/modules/networking/subnets/private/us-east-1a/main.tf"
	for _, maxLen := range []int{0, -1, -100} {
		if got := elidePath(long, maxLen); got != long {
			t.Errorf("elidePath(_, %d) = %q, want unchanged %q", maxLen, got, long)
		}
	}
}

func TestElidePathShortPathUnchanged(t *testing.T) {
	if got := elidePath("go.mod", 40); got != "go.mod" {
		t.Errorf("elidePath(short) = %q, want unchanged", got)
	}
}

func TestElidePathKeepsFilenameVisible(t *testing.T) {
	long := "terraform/modules/networking/subnets/private/us-east-1a/main.tf"
	got := elidePath(long, 24)
	if runeLen(got) > 24 {
		t.Fatalf("elidePath result too long: %q (%d runes)", got, runeLen(got))
	}
	if !strings.HasSuffix(got, "main.tf") {
		t.Fatalf("elidePath should keep the filename visible: %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Fatalf("elidePath should elide with an ellipsis: %q", got)
	}
}

func TestElidePathVeryNarrowTruncatesFilename(t *testing.T) {
	long := "terraform/modules/networking/subnets/private/us-east-1a/main.tf"
	for _, maxLen := range []int{1, 2, 4} {
		got := elidePath(long, maxLen)
		if runeLen(got) > maxLen {
			t.Fatalf("elidePath(_, %d) = %q, exceeds max", maxLen, got)
		}
	}
}

func TestBarWidthForUnknownWidthKeepsDefault(t *testing.T) {
	if got := barWidthFor(0, 20); got != contribBarW {
		t.Errorf("barWidthFor(0, ...) = %d, want %d (unbounded default)", got, contribBarW)
	}
	if got := barWidthFor(-5, 20); got != contribBarW {
		t.Errorf("barWidthFor(-5, ...) = %d, want %d (unbounded default)", got, contribBarW)
	}
}

func TestBarWidthForClampsToMinAndMax(t *testing.T) {
	if got := barWidthFor(200, 10); got != contribBarW {
		t.Errorf("barWidthFor(200, 10) = %d, want capped at %d", got, contribBarW)
	}
	if got := barWidthFor(5, 20); got != minBarW {
		t.Errorf("barWidthFor(5, 20) = %d, want floored at %d", got, minBarW)
	}
	if got := barWidthFor(30, 20); got != 10 {
		t.Errorf("barWidthFor(30, 20) = %d, want 10", got)
	}
}

// buildTestDays returns n consecutive zero-filled days ending on `end`,
// giving each day a small nonzero count so the heatmap has visible cells.
func buildTestDays(end time.Time, n int) []aggregate.DayCount {
	days := make([]aggregate.DayCount, n)
	start := end.AddDate(0, 0, -(n - 1))
	for i := 0; i < n; i++ {
		days[i] = aggregate.DayCount{Date: start.AddDate(0, 0, i), Count: (i % 3) + 1}
	}
	return days
}

func TestHeatmapUnknownWidthShowsAllColumns(t *testing.T) {
	now := time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC)
	days := buildTestDays(now, 90) // spans many week-columns
	var buf bytes.Buffer
	Dashboard(&buf, Model{
		Days:         days,
		TotalCommits: aggregate.TotalCommits(days),
		Now:          now,
		Width:        0, // unknown/unbounded
	}, false)

	_, cols, _ := buildGrid(days, now)
	out := buf.String()
	lines := strings.Split(out, "\n")
	var widest int
	for _, l := range lines {
		if n := runeLen(l); n > widest {
			widest = n
		}
	}
	// Every column costs 2 runes (glyph + space); with unknown width nothing
	// should be dropped, so the widest heatmap row should be able to fit the
	// full column count (2-char indent + 2*cols, trimmed of trailing space).
	wantMin := 2 + cols*2 - 1
	if widest < wantMin {
		t.Fatalf("unknown-width heatmap looks truncated: widest line %d runes, want at least %d\n%s", widest, wantMin, out)
	}
}

func TestHeatmapCapsColumnsAndKeepsMostRecent(t *testing.T) {
	now := time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC)
	days := buildTestDays(now, 90)
	_, fullCols, _ := buildGrid(days, now)

	narrowWidth := 20 // forces maxCols well below fullCols
	maxCols := (narrowWidth - 2) / 2
	if maxCols >= fullCols {
		t.Fatalf("test setup invalid: maxCols %d not less than fullCols %d", maxCols, fullCols)
	}

	p := palette{}
	var buf bytes.Buffer
	p.heatmap(&buf, Model{
		Days:         days,
		TotalCommits: aggregate.TotalCommits(days),
		Now:          now,
		Width:        narrowWidth,
	})

	out := buf.String()
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if i >= 7 { // only the 7 grid rows are column-bounded
			break
		}
		gotCols := (runeLen(l) + 1) / 2 // indent already stripped by TrimRight on empties
		if gotCols > maxCols+1 {        // +1 slack: indent isn't a full column
			t.Fatalf("heatmap row %d has %d runes, exceeds cap of %d columns:\n%s", i, runeLen(l), maxCols, out)
		}
	}
	// Today's glyph ('□' when off, since it's always drawn regardless of
	// count) must still be present: the most recent column is kept, not
	// dropped, when the range doesn't fit.
	if !strings.Contains(out, "□") {
		t.Fatalf("narrow-width heatmap dropped today's column:\n%s", out)
	}
}

func TestHeatmapNarrowWidthDoesNotPanic(t *testing.T) {
	now := time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC)
	days := buildTestDays(now, 90)
	for _, w := range []int{1, 2, 3} {
		var buf bytes.Buffer
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Dashboard panicked at width %d: %v", w, r)
				}
			}()
			Dashboard(&buf, Model{
				Days:         days,
				TotalCommits: aggregate.TotalCommits(days),
				Now:          now,
				Width:        w,
			}, false)
		}()
	}
}

func TestWidthOneDoesNotPanicAcrossAllViews(t *testing.T) {
	now := time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC)
	days := buildTestDays(now, 30)

	longName := "a-very-long-branch-name-that-keeps-going-and-going"
	longPath := "terraform/modules/networking/subnets/private/us-east-1a/very/long/main.tf"

	tests := map[string]func(*bytes.Buffer){
		"Dashboard": func(buf *bytes.Buffer) {
			Dashboard(buf, Model{
				Days:         days,
				TotalCommits: aggregate.TotalCommits(days),
				Contributors: []aggregate.Contributor{{Name: longName, Email: "a@example.com", Commits: 9}},
				HotFiles:     []aggregate.FileChurn{{Path: longPath, Commits: 4}},
				Now:          now,
				Width:        1,
			}, false)
		},
		"Contributors": func(buf *bytes.Buffer) {
			Contributors(buf, ContributorsModel{
				Contributors: []aggregate.Contributor{{Name: longName, Email: "a@example.com", Commits: 9}},
				Now:          now,
				Width:        1,
			}, false)
		},
		"Churn": func(buf *bytes.Buffer) {
			Churn(buf, ChurnModel{
				Files: []aggregate.FileChurn{{Path: longPath, Commits: 4}},
				Now:   now,
				Width: 1,
			}, false)
		},
		"Branches": func(buf *bytes.Buffer) {
			Branches(buf, BranchesModel{
				Branches: []gitdata.Branch{{Name: longName, LastCommit: now, LastAuthor: "Ada"}},
				Now:      now,
				Width:    1,
			}, false)
		},
		"Graph": func(buf *bytes.Buffer) {
			Graph(buf, GraphModel{
				Days:         days,
				TotalCommits: aggregate.TotalCommits(days),
				Bucket:       "day",
				Now:          now,
				Width:        1,
			}, false)
		},
	}

	for name, fn := range tests {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s panicked at width 1: %v", name, r)
				}
			}()
			fn(&buf)
		})
	}
}

func TestPeriodLabel(t *testing.T) {
	start := time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 6, 9, 0, 0, 0, 0, time.UTC)
	bucket := aggregate.PeriodCount{Start: start, End: end, Count: 3}

	cases := []struct {
		name       string
		bucketName string
		want       string
	}{
		{"day", "day", "2024-06-03"},
		{"week", "week", "2024-06-03..2024-06-09"},
		{"month", "month", "2024-06"},
		{"default", "unknown", "2024-06-03"},
	}
	for _, tc := range cases {
		if got := periodLabel(bucket, tc.bucketName); got != tc.want {
			t.Errorf("%s: periodLabel = %q, want %q", tc.name, got, tc.want)
		}
	}
}
