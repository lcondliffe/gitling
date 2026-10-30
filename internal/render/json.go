package render

import (
	"encoding/json"
	"io"
	"net/url"
	"time"

	"github.com/lcondliffe/gitling/internal/aggregate"
	"github.com/lcondliffe/gitling/internal/forge"
	"github.com/lcondliffe/gitling/internal/gitdata"
)

// SchemaVersion is bumped only for breaking changes; new keys are additive.
const SchemaVersion = 1

// Repository identifies the repo a single-repo view describes.
type Repository struct {
	Name   string // repo directory name, shared by all its worktrees
	Remote string // origin URL, raw; sanitized on output
}

// jsonHeader leads every JSON document. Repository is null for the workspace view.
type jsonHeader struct {
	SchemaVersion int             `json:"schema_version"`
	View          string          `json:"view"`
	GeneratedAt   string          `json:"generated_at"`
	Repository    *jsonRepository `json:"repository"`
}

type jsonRepository struct {
	Name   string  `json:"name"`
	Remote *string `json:"remote"`
}

// jsonScope is the history window a view was computed over, plus any
// filters (omitted when unfiltered).
type jsonScope struct {
	Range     string `json:"range"`
	DateBasis string `json:"date_basis"`
	gitdata.Scope
}

func header(view string, repo *Repository, now time.Time) jsonHeader {
	h := jsonHeader{SchemaVersion: SchemaVersion, View: view, GeneratedAt: now.Format(time.RFC3339)}
	if repo != nil {
		h.Repository = &jsonRepository{Name: repo.Name, Remote: jsonRemote(repo.Remote)}
	}
	return h
}

// jsonRemote drops credentials from a URL remote and hides local-path remotes,
// which would leak private filesystem paths.
func jsonRemote(s string) *string {
	u, err := url.Parse(s)
	switch {
	case err == nil && u.Host != "" && u.Scheme != "file":
		u.User = nil
		s = u.String()
	case err == nil:
		return nil // empty, a local path, or a file:// URL
	}
	// Otherwise scp-like (git@host:path), which can't carry a password.
	return &s
}

func encode(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// JSON prints the dashboard model as stable, machine-readable data for scripts
// and integrations. It deliberately mirrors the human dashboard sections while
// using snake_case keys and date-only strings for local calendar buckets.
func JSON(w io.Writer, repo Repository, m Model, bucket string, buckets []aggregate.PeriodCount) error {
	pct := (*float64)(nil)
	if m.Growth.HasPct {
		pct = &m.Growth.Pct
	}

	out := jsonModel{
		jsonHeader:   header("dashboard", &repo, m.Now),
		Scope:        jsonScope{m.RangeLabel, string(m.DateBasis), m.Scope},
		Range:        m.RangeLabel,
		Vitals:       jsonVitalsOf(m.Vitals),
		Activity:     jsonActivityOf(m.Days, m.TotalCommits, m.Streak, bucket, buckets),
		Contributors: jsonContributors(m.Contributors),
		Growth: jsonGrowth{
			TotalLOC: m.Growth.TotalLOC,
			Pct:      pct,
			HasPct:   m.Growth.HasPct,
			Series:   m.Growth.Spark,
		},
		HotFiles: jsonHotFiles(m.HotFiles),
		Recent:   jsonRecents(m.Recent),
		OpenPRs:  jsonPRs(m.PRs.Items),
		Forge:    jsonForgeOf(m.PRs),
	}
	return encode(w, out)
}

type jsonModel struct {
	jsonHeader
	Scope        jsonScope         `json:"scope"`
	Range        string            `json:"range"` // kept for compatibility; same as scope.range
	Vitals       jsonVitals        `json:"vitals"`
	Activity     jsonActivity      `json:"activity"`
	Contributors []jsonContributor `json:"contributors"`
	Growth       jsonGrowth        `json:"growth"`
	HotFiles     []jsonHotFile     `json:"hot_files"`
	Recent       []jsonRecent      `json:"recent"`
	OpenPRs      []jsonPR          `json:"open_prs"`
	Forge        *jsonForge        `json:"forge"`
}

// jsonForge says how open_prs was obtained; null when the lookup was off.
// open_prs is only the real answer when status is "ok".
type jsonForge struct {
	CLI       string `json:"cli"`
	Status    string `json:"status"`
	Truncated bool   `json:"truncated"`
	CheckedAt string `json:"checked_at"`
}

func jsonForgeOf(r forge.Result) *jsonForge {
	if r.Status == "" {
		return nil
	}
	return &jsonForge{CLI: r.CLI, Status: string(r.Status), Truncated: r.Truncated, CheckedAt: r.CheckedAt.Format(time.RFC3339)}
}

// jsonPR is one open pull request on the forge.
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
	out.Operation = jsonOperationOf(v.Operation)
	return out
}

func jsonOperationOf(o gitdata.Operation) *jsonOperation {
	if !o.InProgress() {
		return nil
	}
	op := jsonOperation{Kind: o.Kind}
	if o.Total > 0 {
		step, total := o.Step, o.Total
		op.Step, op.Total = &step, &total
	}
	return &op
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

func jsonActivityOf(days []aggregate.DayCount, total, streak int, bucket string, buckets []aggregate.PeriodCount) jsonActivity {
	return jsonActivity{
		Days:         jsonDays(days),
		TotalCommits: total,
		StreakDays:   streak,
		Bucket:       bucket,
		Buckets:      jsonBuckets(buckets),
	}
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

// GraphJSON prints the activity drill-down.
func GraphJSON(w io.Writer, repo Repository, m GraphModel) error {
	return encode(w, struct {
		jsonHeader
		Scope    jsonScope    `json:"scope"`
		Activity jsonActivity `json:"activity"`
	}{
		header("graph", &repo, m.Now),
		jsonScope{m.RangeLabel, string(m.DateBasis), m.Scope},
		jsonActivityOf(m.Days, m.TotalCommits, m.Streak, m.Bucket, m.Buckets),
	})
}

// ChurnJSON prints every file touched in range, most-touched first.
func ChurnJSON(w io.Writer, repo Repository, m ChurnModel) error {
	return encode(w, struct {
		jsonHeader
		Scope jsonScope     `json:"scope"`
		Files []jsonHotFile `json:"files"`
	}{header("churn", &repo, m.Now), jsonScope{m.RangeLabel, string(m.DateBasis), m.Scope}, jsonHotFiles(m.Files)})
}

// ContributorsJSON prints every author in range, most commits first.
func ContributorsJSON(w io.Writer, repo Repository, m ContributorsModel) error {
	return encode(w, struct {
		jsonHeader
		Scope        jsonScope         `json:"scope"`
		Contributors []jsonContributor `json:"contributors"`
	}{header("contributors", &repo, m.Now), jsonScope{m.RangeLabel, string(m.DateBasis), m.Scope}, jsonContributors(m.Contributors)})
}

// jsonBranch mirrors gitdata.Branch. The worktree path is reduced to a flag so
// private checkout locations stay out of the output.
type jsonBranch struct {
	Name       string  `json:"name"`
	Current    bool    `json:"current"`
	Upstream   *string `json:"upstream"`
	Gone       bool    `json:"gone"`
	Merged     bool    `json:"merged"`
	Tip        string  `json:"tip"`
	CheckedOut bool    `json:"checked_out"`
	CompareRef *string `json:"compare_ref"`
	Ahead      *int    `json:"ahead"`
	Behind     *int    `json:"behind"`
	LastCommit *string `json:"last_commit"`
	LastAuthor string  `json:"last_author"`
}

// BranchesJSON prints every local branch.
func BranchesJSON(w io.Writer, repo Repository, m BranchesModel) error {
	branches := make([]jsonBranch, 0, len(m.Branches))
	for _, b := range m.Branches {
		jb := jsonBranch{
			Name: b.Name, Current: b.IsHead, Upstream: optString(b.Upstream), Gone: b.Gone,
			Merged: b.Merged, Tip: b.Tip, CheckedOut: b.Worktree != "",
			LastCommit: jsonTime(b.LastCommit), LastAuthor: b.LastAuthor,
		}
		if b.HasCompare {
			ahead, behind := b.Ahead, b.Behind
			jb.CompareRef, jb.Ahead, jb.Behind = optString(b.CompareRef), &ahead, &behind
		}
		branches = append(branches, jb)
	}
	return encode(w, struct {
		jsonHeader
		Branches []jsonBranch `json:"branches"`
	}{header("branches", &repo, m.Now), branches})
}

// jsonFileChange is one changed path; counts are null for binary files.
type jsonFileChange struct {
	Path       string  `json:"path"`
	OldPath    *string `json:"old_path"`
	Insertions *int    `json:"insertions"`
	Deletions  *int    `json:"deletions"`
	Binary     bool    `json:"binary"`
}

// CompareJSON prints the branch comparison. Files are complete; commits are
// capped, with commit_count and commits_truncated describing the rest.
func CompareJSON(w io.Writer, repo Repository, m CompareModel) error {
	c := m.Compare
	files := make([]jsonFileChange, 0, len(c.Files))
	for _, f := range c.Files {
		jf := jsonFileChange{Path: f.Path, OldPath: optString(f.OldPath), Binary: f.Binary}
		if !f.Binary {
			ins, del := f.Insertions, f.Deletions
			jf.Insertions, jf.Deletions = &ins, &del
		}
		files = append(files, jf)
	}
	return encode(w, struct {
		jsonHeader
		Head             string           `json:"head"`
		Base             string           `json:"base"`
		BaseOID          string           `json:"base_oid"`
		HeadOID          string           `json:"head_oid"`
		MergeBase        string           `json:"merge_base"`
		Shallow          bool             `json:"shallow"`
		CommitCount      int              `json:"commit_count"`
		CommitsTruncated bool             `json:"commits_truncated"`
		Commits          []jsonRecent     `json:"commits"`
		Files            []jsonFileChange `json:"files"`
		Insertions       int              `json:"insertions"`
		Deletions        int              `json:"deletions"`
	}{
		header("compare", &repo, m.Now), m.Head, c.Base, c.BaseOID, c.HeadOID, c.MergeBase, m.Shallow,
		c.CommitCount, c.CommitCount > len(c.Commits), jsonRecents(c.Commits), files, c.Insertions, c.Deletions,
	})
}

// jsonCheckout is one workspace row. Status is null when the checkout couldn't
// be probed, with the reason in error.
type jsonCheckout struct {
	Name          string              `json:"name"`
	Error         *string             `json:"error"`
	Status        *jsonCheckoutStatus `json:"status"`
	FetchFailed   bool                `json:"fetch_failed"`
	OpenPRs       int                 `json:"open_prs"`
	OpenPRsCapped bool                `json:"open_prs_capped"` // same as forge.truncated
	Forge         *jsonForge          `json:"forge"`
}

// jsonCheckoutStatus is the Repo.Status subset of jsonVitals.
type jsonCheckoutStatus struct {
	Branch      string         `json:"branch"`
	Detached    bool           `json:"detached"`
	HasUpstream bool           `json:"has_upstream"`
	Ahead       int            `json:"ahead"`
	Behind      int            `json:"behind"`
	Operation   *jsonOperation `json:"operation"`
	LastFetch   *string        `json:"last_fetch"`
	DirtyFiles  int            `json:"dirty_files"`
	Staged      int            `json:"staged"`
	Modified    int            `json:"modified"`
	Untracked   int            `json:"untracked"`
	Conflicts   int            `json:"conflicts"`
}

// ReposJSON prints the multi-repo workspace overview.
func ReposJSON(w io.Writer, m ReposModel) error {
	rows := make([]jsonCheckout, 0, len(m.Rows))
	for _, r := range m.Rows {
		jc := jsonCheckout{Name: r.Name, Error: optString(r.Err), FetchFailed: r.FetchFailed, OpenPRs: len(r.PRs.Items), OpenPRsCapped: r.PRs.Truncated, Forge: jsonForgeOf(r.PRs)}
		if r.Err == "" {
			v := r.Vitals
			jc.Status = &jsonCheckoutStatus{
				Branch: v.Branch, Detached: v.Detached, HasUpstream: v.HasUpstream, Ahead: v.Ahead, Behind: v.Behind,
				Operation: jsonOperationOf(v.Operation), LastFetch: jsonTime(v.LastFetch), DirtyFiles: v.DirtyFiles,
				Staged: v.Staged, Modified: v.Modified, Untracked: v.Untracked, Conflicts: v.Conflicts,
			}
		}
		rows = append(rows, jc)
	}
	return encode(w, struct {
		jsonHeader
		Hidden    int            `json:"hidden"`
		Checkouts []jsonCheckout `json:"checkouts"`
	}{header("workspace", nil, m.Now), m.Hidden, rows})
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
