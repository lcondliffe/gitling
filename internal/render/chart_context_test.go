package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/lcondliffe/gitling/internal/aggregate"
)

func TestHeatmapVisibleInterval(t *testing.T) {
	m := goldenModel()
	m.Width = 20
	var b bytes.Buffer
	palette{}.heatmap(&b, m)
	for _, want := range []string{"requested", "2024-04-07", "visible", "2024-04-14", "Jun 15", "omitted", "peak 8/day", "□ today"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q:\n%s", want, b.String())
		}
	}
	for _, line := range strings.Split(b.String(), "\n") {
		if visibleLen(line) > m.Width {
			t.Errorf("overwide: %q", line)
		}
	}
}

func TestDisplayBucketsPreserveRangeAndTotals(t *testing.T) {
	days := goldenDays()
	for _, kind := range []string{"day", "week", "month"} {
		in := aggregate.BucketCounts(days, kind)
		for _, width := range []int{1, 7, 40, 0} {
			out := displayBuckets(in, width)
			if width > 0 && len(out) > width {
				t.Fatalf("%d > %d", len(out), width)
			}
			if !out[0].Start.Equal(in[0].Start) || !out[len(out)-1].End.Equal(in[len(in)-1].End) {
				t.Fatal("range lost")
			}
			total := 0
			for i, b := range out {
				total += b.Count
				if i > 0 && !out[i-1].End.Before(b.Start) {
					t.Fatal("overlap")
				}
			}
			if total != aggregate.TotalCommits(days) {
				t.Fatal("counts lost")
			}
		}
	}
}

func TestGoldenGraphBounded(t *testing.T) {
	m := goldenGraphModel()
	m.Width = 40
	m.Bucket = "day"
	m.Buckets = aggregate.BucketCounts(m.Days, m.Bucket)
	var b bytes.Buffer
	Graph(&b, m, false)
	for _, want := range []string{"summed", "commits/bar", "2024-04-07", "Jun 15"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, line := range strings.Split(b.String(), "\n") {
		if visibleLen(line) > m.Width {
			t.Errorf("overwide: %q", line)
		}
	}
	checkGolden(t, "graph-bounded.golden.txt", b.Bytes())
}

func TestGrowthScope(t *testing.T) {
	m := goldenModel()
	m.Width = 40
	m.Shallow = true
	out := strings.Join(palette{}.growthLines(m, 38), "\n")
	for _, want := range []string{"approx", "historical net", "HEAD", "6mo", "shallow", "not current LOC", "min", "max"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if visibleLen(line) > 38 {
			t.Errorf("overwide: %q", line)
		}
	}
}

func TestDateIntervalYearContext(t *testing.T) {
	a := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	b := a.AddDate(0, 0, 1)
	if got := dateInterval(a, b); got != "2023-12-31..2024-01-01" {
		t.Fatal(got)
	}
}

func TestGraphNarrowCountsAndDateBasis(t *testing.T) {
	for _, kind := range []string{"day", "week", "month"} {
		for _, color := range []bool{false, true} {
			m := goldenGraphModel()
			m.Width, m.Bucket, m.DateBasis = 20, kind, aggregate.CommitDate
			m.Buckets = aggregate.BucketCounts(m.Days, kind)
			var b bytes.Buffer
			Graph(&b, m, color)
			for _, line := range strings.Split(b.String(), "\n") {
				if visibleLen(line) > m.Width {
					t.Errorf("%s: overwide %q", kind, line)
				}
			}
			if !strings.Contains(b.String(), "commit dates") {
				t.Fatal("date basis missing")
			}
		}
	}
}
