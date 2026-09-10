package render

import (
	"fmt"
	"time"

	"github.com/lcondliffe/gitling/internal/aggregate"
)

// dateInterval retains the year at the left, repeating it at year boundaries.
func dateInterval(start, end time.Time) string {
	right := "Jan 02"
	if start.Year() != end.Year() {
		right = "2006-01-02"
	}
	return start.Format("2006-01-02") + ".." + end.Format(right)
}

func historyScope(basis aggregate.DateBasis) string {
	if basis == "" {
		basis = aggregate.AuthorDate
	}
	return "HEAD non-merge; " + string(basis) + " dates"
}

// displayBuckets groups contiguous input buckets into at most columns bars.
// Sum counts, not averages: every requested commit remains represented. Input
// endpoints are retained, including partial calendar buckets from the caller.
func displayBuckets(in []aggregate.PeriodCount, columns int) []aggregate.PeriodCount {
	if columns <= 0 || len(in) <= columns {
		return in
	}
	out := make([]aggregate.PeriodCount, 0, columns)
	for i := 0; i < columns; i++ {
		lo, hi := i*len(in)/columns, (i+1)*len(in)/columns
		b := aggregate.PeriodCount{Start: in[lo].Start, End: in[hi-1].End}
		for _, v := range in[lo:hi] {
			b.Count += v.Count
		}
		out = append(out, b)
	}
	return out
}

func (p palette) growthContext(m Model, width int) []string {
	g := m.Growth
	lines := p.textLines(fmt.Sprintf("approx historical net-line change: %s (not current LOC)", humanInt(g.TotalLOC)), width)
	lines = append(lines, p.textLines(historyScope(m.DateBasis)+"; cumulative", width)...)
	if m.Shallow {
		lines = append(lines, p.textLines("shallow: incomplete history", width)...)
	}
	lines = append(lines, p.textLines("6mo window: "+dateInterval(m.Now.AddDate(0, -6, 0), m.Now), width)...)
	vals := g.Spark
	if width > 0 && len(vals) > width {
		// These are cumulative samples, not independent deltas: keep each group's
		// endpoint rather than summing cumulative values or clipping off the start.
		sampled := make([]int, width)
		for i := range sampled {
			sampled[i] = vals[(i+1)*len(vals)/width-1]
		}
		vals = sampled
		lines = append(lines, p.textLines("resampled at bucket endpoints", width)...)
	}
	if len(vals) > 0 {
		low, high := vals[0], vals[0]
		for _, v := range vals {
			low = min(low, v)
			high = max(high, v)
		}
		lines = append(lines, p.growthChart(vals, growthChartHeight)...)
		lines = append(lines, p.textLines(fmt.Sprintf("scale: min %s / max %s", humanInt(low), humanInt(high)), width)...)
	}
	if g.HasPct {
		lines = append(lines, p.textLines(fmt.Sprintf("%+.1f%% vs 6mo baseline", g.Pct), width)...)
	} else {
		lines = append(lines, p.textLines("6mo baseline unavailable", width)...)
	}
	return lines
}
