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
