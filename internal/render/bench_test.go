package render

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/lcondliffe/gitling/internal/aggregate"
	"github.com/lcondliffe/gitling/internal/gitdata"
)

// syntheticCommitCount matches the ~50k-commit scale called out in the
// "under a second" acceptance criterion.
const syntheticCommitCount = 50000

// syntheticAuthors and syntheticFiles bound the cardinality of generated
// identities/paths so the aggregate maps look like a real, moderately large
// repository rather than 50k unique everything.
const (
	syntheticAuthors = 40
	syntheticFiles   = 800
)

// genSyntheticCommits builds a deterministic (seeded) slice of n commits with
// author/commit times spread over roughly two years, varied authors and
// touched files, and non-trivial insertion/deletion counts. It never shells
// out to git, so it can run anywhere (CI, laptops, sandboxes) at any scale.
func genSyntheticCommits(n int) []gitdata.Commit {
	r := rand.New(rand.NewSource(42))

	authors := make([][2]string, syntheticAuthors)
	for i := range authors {
		authors[i] = [2]string{
			fmt.Sprintf("Author %02d", i),
			fmt.Sprintf("author%02d@example.com", i),
		}
	}
	files := make([]string, syntheticFiles)
	dirs := []string{"internal/render", "internal/aggregate", "internal/gitdata", "internal/cache", "cmd/gitling", "docs"}
	for i := range files {
		files[i] = fmt.Sprintf("%s/file%03d.go", dirs[i%len(dirs)], i)
	}

	end := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	start := end.AddDate(-2, 0, 0)
	spanSeconds := int64(end.Sub(start).Seconds())

	commits := make([]gitdata.Commit, n)
	// Commits arrive newest-first from git log, so generate offsets and sort
	// descending to match that contract.
	offsets := make([]int64, n)
	for i := range offsets {
		offsets[i] = r.Int63n(spanSeconds)
	}
	// Offsets represent "seconds since start"; newest first means largest
	// offset first, matching git log order.
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] > offsets[j] })

	for i := 0; i < n; i++ {
		at := start.Add(time.Duration(offsets[i]) * time.Second)
		ct := at.Add(time.Duration(r.Intn(3600)) * time.Second)
		a := authors[r.Intn(len(authors))]

		numFiles := 1 + r.Intn(6)
		touched := make([]string, 0, numFiles)
		seen := map[int]bool{}
		for len(touched) < numFiles {
			idx := r.Intn(len(files))
			if seen[idx] {
				continue
			}
			seen[idx] = true
			touched = append(touched, files[idx])
		}

		commits[i] = gitdata.Commit{
			Hash:        fmt.Sprintf("%040x", i+1),
			AuthorName:  a[0],
			AuthorEmail: a[1],
			AuthorTime:  at,
			CommitTime:  ct,
			Insertions:  r.Intn(120),
			Deletions:   r.Intn(60),
			Files:       touched,
		}
	}
	return commits
}

// buildPipelineModel runs the full aggregate+model-assembly pipeline used by
// the cmd layer: merge commits into a fresh Aggregates, then derive every
// panel's data for a Dashboard render.
func buildPipelineModel(commits []gitdata.Commit, now time.Time) Model {
	agg := aggregate.New()
	agg.Merge(commits, aggregate.AuthorDate)

	since := now.AddDate(0, 0, -13*7) // 13 weeks, matching a typical dashboard range
	days := agg.DailyCounts(since, now)

	return Model{
		Vitals:       gitdata.Vitals{Branch: "main", HasUpstream: true, BranchCount: 3},
		RangeLabel:   "last 13 weeks",
		Days:         days,
		TotalCommits: aggregate.TotalCommits(days),
		Streak:       aggregate.Streak(days),
		Contributors: agg.TopContributors(since, now, 5),
		Growth:       agg.BuildGrowth(now),
		HotFiles:     agg.HotFiles(since, now, 5),
		Now:          now,
	}
}

// BenchmarkPipeline50kCommits measures merge + panel derivation + Dashboard
// render end to end on a synthetic ~50k-commit history, so cache-cold-vs-warm
// and layout-cost regressions in the aggregate/render pipeline show up here.
func BenchmarkPipeline50kCommits(b *testing.B) {
	commits := genSyntheticCommits(syntheticCommitCount)
	now := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m := buildPipelineModel(commits, now)
		Dashboard(io.Discard, m, false)
	}
}

// BenchmarkAggregateMerge50kCommits isolates the aggregate.Merge cost (the
// "cold cache" path: folding raw commits into day buckets) from rendering.
func BenchmarkAggregateMerge50kCommits(b *testing.B) {
	commits := genSyntheticCommits(syntheticCommitCount)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agg := aggregate.New()
		agg.Merge(commits, aggregate.AuthorDate)
	}
}

// TestPipelineUnder50kCommitsIsFast guards the "under a second" acceptance
// criterion: aggregating ~50k synthetic commits and rendering the full
// dashboard must comfortably clear a 1s budget. The threshold is deliberately
// generous (real hardware should be far faster) to avoid flaking on slow CI
// runners; it exists to catch gross regressions, not to micro-benchmark.
func TestPipelineUnder50kCommitsIsFast(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 50k-commit pipeline timing test in -short mode")
	}

	commits := genSyntheticCommits(syntheticCommitCount)
	now := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

	start := time.Now()
	m := buildPipelineModel(commits, now)
	var buf bytes.Buffer
	Dashboard(&buf, m, false)
	elapsed := time.Since(start)

	if buf.Len() == 0 {
		t.Fatalf("Dashboard produced no output for synthetic %d-commit history", syntheticCommitCount)
	}
	const budget = 1 * time.Second
	if elapsed > budget {
		t.Fatalf("aggregate+render pipeline on %d synthetic commits took %s, want <= %s", syntheticCommitCount, elapsed, budget)
	}
	t.Logf("aggregate+render pipeline on %d synthetic commits took %s", syntheticCommitCount, elapsed)
}
