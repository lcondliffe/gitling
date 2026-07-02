package render

import (
	"encoding/json"
	"io"
	"time"

	"github.com/lcondliffe/gitling/internal/aggregate"
)

// JSON prints the dashboard model as stable, machine-readable data for scripts
// and integrations. It deliberately mirrors the human dashboard sections while
// using snake_case keys and date-only strings for local calendar buckets.
func JSON(w io.Writer, m Model, bucket string, buckets []aggregate.PeriodCount) error {
	pct := (*float64)(nil)
	if m.Growth.HasPct {
		pct = &m.Growth.Pct
	}

	out := jsonModel{
		Range:       m.RangeLabel,
		GeneratedAt: m.Now.Format(time.RFC3339),
		Vitals: jsonVitals{
			Branch:      m.Vitals.Branch,
			Detached:    m.Vitals.Detached,
			HasUpstream: m.Vitals.HasUpstream,
			Ahead:       m.Vitals.Ahead,
			Behind:      m.Vitals.Behind,
			DirtyFiles:  m.Vitals.DirtyFiles,
			StashCount:  m.Vitals.StashCount,
			BranchCount: m.Vitals.BranchCount,
		},
		Activity: jsonActivity{
			Days:         jsonDays(m.Days),
			TotalCommits: m.TotalCommits,
			StreakDays:   m.Streak,
			Bucket:       bucket,
			Buckets:      jsonBuckets(buckets),
		},
		Contributors: jsonContributors(m.Contributors),
		Growth: jsonGrowth{
			TotalLOC: m.Growth.TotalLOC,
			Pct:      pct,
			HasPct:   m.Growth.HasPct,
			Series:   m.Growth.Spark,
		},
		HotFiles: jsonHotFiles(m.HotFiles),
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

type jsonModel struct {
	Range        string            `json:"range"`
	GeneratedAt  string            `json:"generated_at"`
	Vitals       jsonVitals        `json:"vitals"`
	Activity     jsonActivity      `json:"activity"`
	Contributors []jsonContributor `json:"contributors"`
	Growth       jsonGrowth        `json:"growth"`
	HotFiles     []jsonHotFile     `json:"hot_files"`
}

type jsonVitals struct {
	Branch      string `json:"branch"`
	Detached    bool   `json:"detached"`
	HasUpstream bool   `json:"has_upstream"`
	Ahead       int    `json:"ahead"`
	Behind      int    `json:"behind"`
	DirtyFiles  int    `json:"dirty_files"`
	StashCount  int    `json:"stash_count"`
	BranchCount int    `json:"branch_count"`
}

type jsonActivity struct {
	Days         []jsonDay    `json:"days"`
	TotalCommits int          `json:"total_commits"`
	StreakDays   int          `json:"streak_days"`
	Bucket       string       `json:"bucket"`
	Buckets      []jsonBucket `json:"buckets"`
}

type jsonDay struct {
	Date    string `json:"date"`
	Commits int    `json:"commits"`
}

type jsonBucket struct {
	Start   string `json:"start"`
	End     string `json:"end"`
	Commits int    `json:"commits"`
}

type jsonGrowth struct {
	TotalLOC int      `json:"total_loc"`
	Pct      *float64 `json:"pct"`
	HasPct   bool     `json:"has_pct"`
	Series   []int    `json:"series"`
}

type jsonContributor struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Commits int    `json:"commits"`
}

type jsonHotFile struct {
	Path    string `json:"path"`
	Commits int    `json:"commits"`
}

func jsonDays(days []aggregate.DayCount) []jsonDay {
	out := make([]jsonDay, 0, len(days))
	for _, d := range days {
		out = append(out, jsonDay{
			Date:    d.Date.Format("2006-01-02"),
			Commits: d.Count,
		})
	}
	return out
}

func jsonBuckets(buckets []aggregate.PeriodCount) []jsonBucket {
	out := make([]jsonBucket, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, jsonBucket{
			Start:   b.Start.Format("2006-01-02"),
			End:     b.End.Format("2006-01-02"),
			Commits: b.Count,
		})
	}
	return out
}

func jsonContributors(contributors []aggregate.Contributor) []jsonContributor {
	out := make([]jsonContributor, 0, len(contributors))
	for _, c := range contributors {
		out = append(out, jsonContributor{
			Email:   c.Email,
			Name:    c.Name,
			Commits: c.Commits,
		})
	}
	return out
}

func jsonHotFiles(files []aggregate.FileChurn) []jsonHotFile {
	out := make([]jsonHotFile, 0, len(files))
	for _, f := range files {
		out = append(out, jsonHotFile{
			Path:    f.Path,
			Commits: f.Commits,
		})
	}
	return out
}
