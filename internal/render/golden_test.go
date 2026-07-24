package render

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lcondliffe/gitling/internal/aggregate"
	"github.com/lcondliffe/gitling/internal/gitdata"
)

// update regenerates golden files: go test ./internal/render -run TestGolden -update
var update = flag.Bool("update", false, "update golden files")

// goldenNow is a fixed instant so heatmap "today" markers and humanAgo labels
// are deterministic across runs.
var goldenNow = time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

// goldenDays builds a deterministic, fully zero-filled day series covering ten
// weeks up to goldenNow, with a repeating but non-trivial commit pattern so the
// heatmap exercises every glyph level.
func goldenDays() []aggregate.DayCount {
	start := goldenNow.AddDate(0, 0, -69) // 10 full weeks back
	pattern := []int{0, 1, 2, 4, 8, 3, 0}
	var days []aggregate.DayCount
	for d, i := start, 0; !d.After(goldenNow); d, i = d.AddDate(0, 0, 1), i+1 {
		days = append(days, aggregate.DayCount{Date: d, Count: pattern[i%len(pattern)]})
	}
	return days
}

func goldenContributors() []aggregate.Contributor {
	return []aggregate.Contributor{
		{Email: "ada@example.com", Name: "Ada Lovelace", Commits: 42},
		{Email: "alan@example.com", Name: "Alan Turing", Commits: 27},
		{Email: "grace@example.com", Name: "Grace Hopper", Commits: 15},
		{Email: "margaret@example.com", Name: "Margaret Hamilton", Commits: 9},
		{Email: "katherine@example.com", Name: "Katherine Johnson", Commits: 3},
	}
}

func goldenHotFiles() []aggregate.FileChurn {
	return []aggregate.FileChurn{
		{Path: "internal/render/render.go", Commits: 31},
		{Path: "internal/aggregate/aggregate.go", Commits: 18},
		{Path: "cmd/gitling/main.go", Commits: 12},
		{Path: "go.mod", Commits: 2},
	}
}

func goldenGrowth() aggregate.Growth {
	spark := make([]int, 24)
	loc := 8000
	for i := range spark {
		loc += 400 + (i%5)*37
		spark[i] = loc
	}
	return aggregate.Growth{
		TotalLOC: loc,
		Pct:      18.4,
		HasPct:   true,
		Spark:    spark,
	}
}

func goldenVitals() gitdata.Vitals {
	return gitdata.Vitals{
		Branch:      "main",
		HasUpstream: true,
		Ahead:       2,
		Behind:      1,
		DirtyFiles:  3,
		StashCount:  1,
		BranchCount: 6,
	}
}

func goldenModel() Model {
	days := goldenDays()
	return Model{
		Vitals:       goldenVitals(),
		RangeLabel:   "last 10 weeks",
		Days:         days,
		TotalCommits: aggregate.TotalCommits(days),
		Streak:       3,
		Contributors: goldenContributors(),
		Growth:       goldenGrowth(),
		HotFiles:     goldenHotFiles(),
		Now:          goldenNow,
	}
}

func goldenGraphModel() GraphModel {
	days := goldenDays()
	buckets := aggregate.BucketCounts(days, "week")
	return GraphModel{
		RangeLabel:   "last 10 weeks",
		Bucket:       "week",
		Days:         days,
		Buckets:      buckets,
		TotalCommits: aggregate.TotalCommits(days),
		Streak:       3,
		Now:          goldenNow,
	}
}

func goldenChurnModel() ChurnModel {
	return ChurnModel{
		RangeLabel: "last 1y",
		Files:      goldenHotFiles(),
		Now:        goldenNow,
	}
}

func goldenContributorsModel() ContributorsModel {
	return ContributorsModel{
		RangeLabel:   "last 1y",
		Contributors: goldenContributors(),
		Now:          goldenNow,
	}
}

func goldenBranchesModel() BranchesModel {
	return BranchesModel{
		Now: goldenNow,
		Branches: []gitdata.Branch{
			{
				Name: "main", IsHead: true, Upstream: "origin/main", HasCompare: true,
				CompareRef: "origin/main", LastCommit: goldenNow.Add(-2 * time.Hour), LastAuthor: "Ada Lovelace",
			},
			{
				Name: "feature/heatmap", Upstream: "", HasCompare: true, CompareRef: "main",
				Ahead: 5, Behind: 1, LastCommit: goldenNow.Add(-3 * 24 * time.Hour), LastAuthor: "Alan Turing",
			},
			{
				Name: "stale/old-experiment", Gone: true,
				LastCommit: goldenNow.Add(-40 * 24 * time.Hour), LastAuthor: "Grace Hopper",
			},
			{
				Name: "chore/no-upstream-in-sync", HasCompare: false,
				LastCommit: goldenNow.Add(-5 * time.Minute), LastAuthor: "Margaret Hamilton",
			},
		},
	}
}

// checkGolden renders got against the golden file at testdata/name, updating
// it in place when -update is passed.
func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with -update to create it): %v", path, err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("golden mismatch for %s (run `go test ./internal/render -run TestGolden -update` to refresh, then review the diff):\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}

func TestGoldenDashboard(t *testing.T) {
	var buf bytes.Buffer
	Dashboard(&buf, goldenModel(), false)
	checkGolden(t, "dashboard.golden.txt", buf.Bytes())
}

func TestGoldenGraph(t *testing.T) {
	var buf bytes.Buffer
	Graph(&buf, goldenGraphModel(), false)
	checkGolden(t, "graph.golden.txt", buf.Bytes())
}

func TestGoldenChurn(t *testing.T) {
	var buf bytes.Buffer
	Churn(&buf, goldenChurnModel(), false)
	checkGolden(t, "churn.golden.txt", buf.Bytes())
}

func TestGoldenContributors(t *testing.T) {
	var buf bytes.Buffer
	Contributors(&buf, goldenContributorsModel(), false)
	checkGolden(t, "contributors.golden.txt", buf.Bytes())
}

func TestGoldenBranches(t *testing.T) {
	var buf bytes.Buffer
	Branches(&buf, goldenBranchesModel(), false)
	checkGolden(t, "branches.golden.txt", buf.Bytes())
}
