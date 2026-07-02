// Package aggregate turns raw commits into the per-panel metrics the dashboard
// needs, and holds the in-memory shape that the cache persists.
//
// The unit of aggregation is the local calendar day. Storing per-day buckets
// (rather than range-scoped totals) means a single cached blob can answer any
// --since range by summing the days that fall inside it, so changing the range
// never invalidates the cache.
package aggregate

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/lcondliffe/gitling/internal/gitdata"
)

const dayFmt = "2006-01-02" // lexical order == chronological order

// DayBucket holds everything derived for a single calendar day.
type DayBucket struct {
	Commits    int
	Insertions int
	Deletions  int
	Authors    map[string]int // author email -> commits that day
	Files      map[string]int // path -> commits touching it that day
}

// Aggregates is the full-history, range-independent rollup. It is the value the
// cache serializes.
type Aggregates struct {
	Days        map[string]DayBucket // key: "2006-01-02"
	AuthorNames map[string]string    // email -> display name
}

// New returns an empty Aggregates ready to Merge into.
func New() *Aggregates {
	return &Aggregates{
		Days:        map[string]DayBucket{},
		AuthorNames: map[string]string{},
	}
}

// Merge folds commits into the rollup. It is additive, so callers can pass only
// the commits newer than the last cached hash. Commits are expected newest-first
// (git log order); author name is recorded from the newest commit seen per email.
func (a *Aggregates) Merge(commits []gitdata.Commit) {
	if a.Days == nil {
		a.Days = map[string]DayBucket{}
	}
	if a.AuthorNames == nil {
		a.AuthorNames = map[string]string{}
	}
	for _, c := range commits {
		day := c.AuthorTime.Local().Format(dayFmt)
		b := a.Days[day]
		if b.Authors == nil {
			b.Authors = map[string]int{}
		}
		if b.Files == nil {
			b.Files = map[string]int{}
		}
		b.Commits++
		b.Insertions += c.Insertions
		b.Deletions += c.Deletions
		b.Authors[c.AuthorEmail]++
		seen := make(map[string]bool, len(c.Files))
		for _, f := range c.Files {
			if seen[f] {
				continue
			}
			seen[f] = true
			b.Files[f]++
		}
		a.Days[day] = b

		if c.AuthorName != "" {
			if _, ok := a.AuthorNames[c.AuthorEmail]; !ok {
				a.AuthorNames[c.AuthorEmail] = c.AuthorName
			}
		}
	}
}

// DayCount is one day's commit total in a contiguous, zero-filled series.
type DayCount struct {
	Date  time.Time
	Count int
}

// PeriodCount is a day/week/month activity bucket for drill-down views.
type PeriodCount struct {
	Start time.Time
	End   time.Time
	Count int
}

// Contributor is one author's commit total within a range.
type Contributor struct {
	Email   string
	Name    string
	Commits int
}

// FileChurn is one file's commit-touch count within a range.
type FileChurn struct {
	Path    string
	Commits int
}

// Growth is the codebase-growth panel's data.
type Growth struct {
	TotalLOC int     // cumulative net (insertions-deletions) over all history
	Pct      float64 // percent change over the 6-month lookback
	HasPct   bool    // false when there is no pre-6mo baseline to compare against
	Spark    []int   // cumulative net LOC at the end of each week in the range
}

// DailyCounts returns a zero-filled per-day commit series for [since, until]
// inclusive (local days), oldest first.
func (a *Aggregates) DailyCounts(since, until time.Time) []DayCount {
	start, end := truncateDay(since), truncateDay(until)
	var out []DayCount
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		out = append(out, DayCount{Date: d, Count: a.Days[d.Format(dayFmt)].Commits})
	}
	return out
}

// TotalCommits sums commits across a day series.
func TotalCommits(days []DayCount) int {
	n := 0
	for _, d := range days {
		n += d.Count
	}
	return n
}

// BucketCounts folds a contiguous daily series into day, week, or month buckets.
// Weeks start on Monday, which reads more naturally in long-range terminal
// summaries than the Sunday-first heatmap layout.
func BucketCounts(days []DayCount, bucket string) []PeriodCount {
	if len(days) == 0 {
		return nil
	}
	var out []PeriodCount
	var cur PeriodCount
	for i, d := range days {
		start := bucketStart(d.Date, bucket)
		if i == 0 || !start.Equal(cur.Start) {
			if i > 0 {
				out = append(out, cur)
			}
			cur = PeriodCount{Start: start, End: bucketEnd(start, bucket)}
		}
		cur.Count += d.Count
	}
	out = append(out, cur)
	return out
}

// Streak counts consecutive active days ending at the most recent day. A blank
// final day (today, no commits yet) does not break the streak.
func Streak(days []DayCount) int {
	streak := 0
	for i := len(days) - 1; i >= 0; i-- {
		if days[i].Count > 0 {
			streak++
			continue
		}
		if i == len(days)-1 { // allow an empty "today"
			continue
		}
		break
	}
	return streak
}

// TopContributors ranks authors by commit count within [since, until], limited
// to n (n<=0 means all).
//
// Identities are stored by email but coalesced by case-insensitive display name
// for presentation: the common real-world case is one person committing under
// several emails, and for an at-a-glance view merging them is friendlier than
// showing the same name twice. (.mailmap, applied upstream via %aN/%aE, is the
// precise mechanism; this is the pragmatic fallback when none exists.)
func (a *Aggregates) TopContributors(since, until time.Time, n int) []Contributor {
	sk, uk := truncateDay(since).Format(dayFmt), truncateDay(until).Format(dayFmt)
	perEmail := map[string]int{}
	for day, b := range a.Days {
		if day < sk || day > uk {
			continue
		}
		for email, c := range b.Authors {
			perEmail[email] += c
		}
	}
	byName := map[string]*Contributor{}
	for email, c := range perEmail {
		name := a.AuthorNames[email]
		if name == "" {
			name = email
		}
		key := strings.ToLower(strings.TrimSpace(name))
		if existing, ok := byName[key]; ok {
			existing.Commits += c
		} else {
			byName[key] = &Contributor{Email: email, Name: name, Commits: c}
		}
	}
	out := make([]Contributor, 0, len(byName))
	for _, c := range byName {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Commits != out[j].Commits {
			return out[i].Commits > out[j].Commits
		}
		return out[i].Name < out[j].Name
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// HotFiles ranks files by the number of commits touching them within
// [since, until], limited to n (n<=0 means all).
func (a *Aggregates) HotFiles(since, until time.Time, n int) []FileChurn {
	sk, uk := truncateDay(since).Format(dayFmt), truncateDay(until).Format(dayFmt)
	totals := map[string]int{}
	for day, b := range a.Days {
		if day < sk || day > uk {
			continue
		}
		for path, c := range b.Files {
			totals[path] += c
		}
	}
	out := make([]FileChurn, 0, len(totals))
	for path, c := range totals {
		out = append(out, FileChurn{Path: path, Commits: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Commits != out[j].Commits {
			return out[i].Commits > out[j].Commits
		}
		return out[i].Path < out[j].Path
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// BuildGrowth computes the codebase-growth panel. All of it describes the same
// 6-month window: TotalLOC (cumulative net lines to now), the percent change vs
// 6 months ago, and Spark — cumulative LOC sampled across those 6 months, which
// render draws as a multi-row min-max bar chart (see render.growthChart).
func (a *Aggregates) BuildGrowth(until time.Time) Growth {
	now := truncateDay(until)
	g := Growth{TotalLOC: clampZero(a.netUpTo(now))}

	baseDay := now.AddDate(0, -6, 0)
	if a.hasDataBefore(baseDay) {
		base := a.netUpTo(baseDay)
		cur := a.netUpTo(now)
		if base > 0 {
			g.Pct = float64(cur-base) / float64(base) * 100
			g.HasPct = true
		}
	}

	const maxSparkPoints = 36
	totalDays := DaysBetween(baseDay, now)
	if totalDays < 1 {
		totalDays = 1
	}
	points := totalDays
	if points > maxSparkPoints {
		points = maxSparkPoints
	}
	for i := 1; i <= points; i++ {
		off := int(math.Round(float64(i) * float64(totalDays) / float64(points)))
		d := baseDay.AddDate(0, 0, off)
		if d.After(now) {
			d = now
		}
		g.Spark = append(g.Spark, clampZero(a.netUpTo(d)))
	}
	return g
}

// netUpTo sums net line delta for all days on or before t.
func (a *Aggregates) netUpTo(t time.Time) int {
	key := t.Format(dayFmt)
	sum := 0
	for day, b := range a.Days {
		if day <= key {
			sum += b.Insertions - b.Deletions
		}
	}
	return sum
}

func (a *Aggregates) hasDataBefore(t time.Time) bool {
	key := t.Format(dayFmt)
	for day := range a.Days {
		if day < key {
			return true
		}
	}
	return false
}

func truncateDay(t time.Time) time.Time {
	t = t.Local()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func bucketStart(t time.Time, bucket string) time.Time {
	t = truncateDay(t)
	switch bucket {
	case "week":
		// time.Weekday is Sunday=0; convert so Monday is the start.
		daysSinceMonday := (int(t.Weekday()) + 6) % 7
		return t.AddDate(0, 0, -daysSinceMonday)
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	default:
		return t
	}
}

func bucketEnd(start time.Time, bucket string) time.Time {
	switch bucket {
	case "week":
		return start.AddDate(0, 0, 6)
	case "month":
		return start.AddDate(0, 1, -1)
	default:
		return start
	}
}

func clampZero(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// DaysBetween returns whole local days from a to b, rounding to absorb DST.
func DaysBetween(a, b time.Time) int {
	return int(math.Round(truncateDay(b).Sub(truncateDay(a)).Hours() / 24))
}
