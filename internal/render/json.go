package render

import (
	"encoding/json"
	"io"
	"time"

	"github.com/lcondliffe/gitling/internal/aggregate"
	"github.com/lcondliffe/gitling/internal/forge"
	"github.com/lcondliffe/gitling/internal/gitdata"
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
		Vitals:      jsonVitalsOf(m.Vitals),
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
		Recent:   jsonRecents(m.Recent),
		OpenPRs:  jsonPRs(m.PRs),
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
	Recent       []jsonRecent      `json:"recent"`
	OpenPRs      []jsonPR          `json:"open_prs"`
}

// jsonPR is one open pull request on the forge; the list is empty when there
// is no forge CLI to ask.
type jsonPR struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Author  string `json:"author"`
	Draft   bool   `json:"draft"`
	Updated string `json:"updated"`
}

func jsonPRs(prs []forge.PR) []jsonPR {
	out := make([]jsonPR, 0, len(prs))
	for _, pr := range prs {
		out = append(out, jsonPR{
			Number:  pr.Number,
			Title:   pr.Title,
			Author:  pr.Author,
			Draft:   pr.Draft,
			Updated: pr.Updated.Format(time.RFC3339),
		})
	}
	return out
}

// jsonRecent is one commit at the tip of HEAD. PR is null rather than 0 when no
// pull-request number was found, so consumers can tell "no PR" from "PR 0".
type jsonRecent struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	PR      *int   `json:"pr"`
	Merge   bool   `json:"merge"`
}

// jsonVitals mirrors gitdata.Vitals. Timestamps and the in-progress operation
// are pointers so "never fetched", "no stashes", and "nothing in progress" come
// through as null rather than as a zero date or an empty object.
type jsonVitals struct {
	Branch      string         `json:"branch"`
	Detached    bool           `json:"detached"`
	HasUpstream bool           `json:"has_upstream"`
	Ahead       int            `json:"ahead"`
	Behind      int            `json:"behind"`
	Operation   *jsonOperation `json:"operation"`
	LastFetch   *string        `json:"last_fetch"`

	// staged/modified overlap; only dirty_files is a total (see gitdata.Vitals).
	DirtyFiles int `json:"dirty_files"`
	Staged     int `json:"staged"`
	Modified   int `json:"modified"`
	Untracked  int `json:"untracked"`
	Conflicts  int `json:"conflicts"`

	StashCount  int     `json:"stash_count"`
	OldestStash *string `json:"oldest_stash"`

	BranchCount    int `json:"branch_count"`
	MergedBranches int `json:"merged_branches"`
	GoneBranches   int `json:"gone_branches"`
	StaleBranches  int `json:"stale_branches"`
	StaleAfterDays int `json:"stale_after_days"`
}

// jsonOperation is an in-progress multi-step git operation. step/total are null
// when the operation isn't one git tracks a position for.
type jsonOperation struct {
	Kind  string `json:"kind"`
	Step  *int   `json:"step"`
	Total *int   `json:"total"`
}

func jsonVitalsOf(v gitdata.Vitals) jsonVitals {
	out := jsonVitals{
		Branch:         v.Branch,
		Detached:       v.Detached,
		HasUpstream:    v.HasUpstream,
		Ahead:          v.Ahead,
		Behind:         v.Behind,
		LastFetch:      jsonTime(v.LastFetch),
		DirtyFiles:     v.DirtyFiles,
		Staged:         v.Staged,
		Modified:       v.Modified,
		Untracked:      v.Untracked,
		Conflicts:      v.Conflicts,
		StashCount:     v.StashCount,
		OldestStash:    jsonTime(v.OldestStash),
		BranchCount:    v.BranchCount,
		MergedBranches: v.MergedBranches,
		GoneBranches:   v.GoneBranches,
		StaleBranches:  v.StaleBranches,
		StaleAfterDays: v.StaleAfterDays,
	}
	if v.Operation.InProgress() {
		op := jsonOperation{Kind: v.Operation.Kind}
		if v.Operation.Total > 0 {
			step, total := v.Operation.Step, v.Operation.Total
			op.Step, op.Total = &step, &total
		}
		out.Operation = &op
	}
	return out
}

// jsonTime renders t as RFC 3339, or null for the zero time.
func jsonTime(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.Format(time.RFC3339)
	return &s
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

func jsonRecents(commits []gitdata.RecentCommit) []jsonRecent {
	out := make([]jsonRecent, 0, len(commits))
	for _, c := range commits {
		var pr *int
		if c.PR > 0 {
			n := c.PR
			pr = &n
		}
		out = append(out, jsonRecent{
			Hash:    c.Hash,
			Short:   c.Short,
			Subject: c.Subject,
			Author:  c.Author,
			Date:    c.Time.Format(time.RFC3339),
			PR:      pr,
			Merge:   c.Merge,
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
