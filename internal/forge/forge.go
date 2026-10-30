// Package forge lists the open pull/merge requests for a repository by
// shelling out to the hosting platform's own CLI (`gh` for GitHub, `glab` for
// GitLab). gitling never talks to a forge API itself, so it never handles
// tokens: the CLI is already installed and authenticated, or the Result says
// why not.
package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PR is one open pull/merge request.
type PR struct {
	Number  int
	Title   string
	Author  string
	Draft   bool
	Updated time.Time
	URL     string
	SHA     string // head commit
	Review  Review
	CI      CI
}

// Review is a PR's review state; empty when unknown or nobody has been asked.
type Review string

const (
	ReviewRequested  Review = "review_requested"
	Approved         Review = "approved"
	ChangesRequested Review = "changes_requested"
)

// CI is the state of a PR's checks; empty when unknown. No checks is CINone,
// never passing.
type CI string

const (
	CINone    CI = "none"
	CIPending CI = "pending"
	CIPassing CI = "passing"
	CIFailing CI = "failing"
)

// Options narrows a lookup. Details adds review and CI state, which costs more.
type Options struct {
	Mine    bool // authored by me or awaiting my review
	Details bool
}

// Status says how a lookup went. Only OK means Items is the real answer.
type Status string

const (
	OK              Status = "ok"
	Unsupported     Status = "unsupported_host"
	MissingCLI      Status = "missing_cli"
	Unauthenticated Status = "unauthenticated"
	Timeout         Status = "timeout"
	Failed          Status = "failed"
)

// Result is one forge lookup. The zero value means no lookup was made.
type Result struct {
	CLI       string // "gh", "glab"; empty when the host is unsupported
	Status    Status
	Items     []PR
	Truncated bool   // more are open than Items holds
	Details   Status // review/CI lookup when it is a separate call; empty otherwise
	CheckedAt time.Time
}

// Note is a short human reason for a non-OK status; empty otherwise.
func (r Result) Note() string {
	switch r.Status {
	case Unsupported:
		return "no supported forge for this remote"
	case MissingCLI:
		return r.CLI + " not installed"
	case Unauthenticated:
		return r.CLI + " not logged in"
	case Timeout:
		return r.CLI + " timed out"
	case Failed:
		return r.CLI + " query failed"
	}
	return ""
}

// DetailsNote says why review/CI state is missing; empty when it isn't.
func (r Result) DetailsNote() string {
	if r.Details == "" || r.Details == OK {
		return ""
	}
	return "review/CI unknown: " + Result{CLI: r.CLI, Status: r.Details}.Note()
}

// listTimeout bounds the CLI call. The dashboard is worth waiting a moment
// for, not worth hanging on. A var so tests can shorten it.
var listTimeout = 3 * time.Second

type adapter struct {
	cli   string
	argv  func(project string, limit int) []string
	parse func([]byte) ([]PR, error)
	// mine is one argv tail per personal query; details is appended for
	// review/CI state, or enrich fetches it after.
	mine    [][]string
	details []string
	enrich  func(ctx context.Context, dir, web, path string, prs []PR) Status
}

// kinds are the supported forges, keyed by the name config uses for them.
var kinds = map[string]adapter{
	"github": {"gh", ghArgv, parseGH,
		[][]string{{"--author", "@me"}, {"--search", "review-requested:@me sort:updated-desc"}},
		[]string{"--json", "url,headRefOid,reviewDecision,reviewRequests,statusCheckRollup"}, nil},
	"gitlab": {"glab", glabArgv, parseGLab,
		[][]string{{"--author", "@me"}, {"--reviewer", "@me"}}, nil, glabDetails},
}

// hosts maps a remote's host to its forge kind; AddHost extends it.
var hosts = map[string]string{"github.com": "github", "gitlab.com": "gitlab"}

// AddHost maps host (e.g. a self-managed GitLab) to kind "github" or "gitlab".
func AddHost(host, kind string) error {
	if _, ok := kinds[kind]; !ok {
		return fmt.Errorf("forge host %q: unknown kind %q (want github or gitlab)", host, kind)
	}
	hosts[strings.ToLower(host)] = kind
	return nil
}

// List returns the open PRs for the repo in dir, whose origin remote is
// remoteURL, most recently updated first and capped at limit.
//
// Nothing here is fatal: the panel is a bonus, so a missing or logged-out CLI,
// a timeout or bad output becomes a Status rather than an error.
func List(dir, remoteURL string, limit int, opt Options) Result {
	res := Result{Status: Unsupported, CheckedAt: time.Now()}
	host, web, path := parseRemote(remoteURL)
	if f, ok := kinds[hosts[host]]; ok && path != "" {
		res.CLI = f.cli
		ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
		defer cancel()
		tails := [][]string{nil}
		if opt.Mine {
			tails = f.mine
		}
		var prs []PR
		for _, tail := range tails {
			argv := append(f.argv(web+"/"+path, limit+1), tail...) // one extra reveals truncation
			if opt.Details {
				argv = append(argv, f.details...)
			}
			out, err := run(ctx, dir, argv)
			if res.Status = classify(ctx, err); res.Status != OK {
				return res
			}
			got, err := f.parse(out)
			if err != nil {
				res.Status = Failed
				return res
			}
			prs = append(prs, got...)
		}
		prs = rank(prs)
		if len(prs) > limit {
			prs, res.Truncated = prs[:limit], true
		}
		if opt.Details && f.enrich != nil && len(prs) > 0 {
			res.Details = f.enrich(ctx, dir, web, path, prs)
		}
		res.Items = prs
		return res
	}
	return res
}

func run(ctx context.Context, dir string, argv []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	return cmd.Output()
}

// rank drops repeats (a PR can be both mine and awaiting me) and puts the most
// recently updated first, before any limit is applied.
func rank(prs []PR) []PR {
	seen := map[int]bool{}
	out := prs[:0]
	for _, pr := range prs {
		if !seen[pr.Number] {
			seen[pr.Number] = true
			out = append(out, pr)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// classify maps a CLI run's error to a Status.
func classify(ctx context.Context, err error) Status {
	var exit *exec.ExitError
	switch {
	case err == nil:
		return OK
	case ctx.Err() == context.DeadlineExceeded:
		return Timeout
	case errors.Is(err, exec.ErrNotFound):
		return MissingCLI
	case errors.As(err, &exit) && isAuthError(exit):
		return Unauthenticated
	}
	return Failed
}

// isAuthError spots a logged-out CLI: gh exits 4, glab only says so on stderr.
// ponytail: stderr sniffing; run `<cli> auth status` instead if it misfires.
func isAuthError(exit *exec.ExitError) bool {
	msg := strings.ToLower(string(exit.Stderr))
	return exit.ExitCode() == 4 || strings.Contains(msg, "auth login") ||
		strings.Contains(msg, "not logged in") || strings.Contains(msg, "401")
}

// parseRemote splits a git remote into hostname, web base URL and project path
// (no .git), from a URL (https://host/p, ssh://git@host:22/p) or SCP shorthand
// (git@host:p). Matching is on the host itself, so notgithub.com isn't github.com.
// An HTTP(S) remote keeps its scheme and port in web; SSH ones get https://host.
func parseRemote(remote string) (host, web, path string) {
	remote = strings.TrimSpace(remote)
	if !strings.Contains(remote, "://") {
		// SCP shorthand: everything before the first colon, minus any user@.
		var ok bool
		if host, path, ok = strings.Cut(remote, ":"); !ok {
			return "", "", ""
		}
		if _, after, ok := strings.Cut(host, "@"); ok {
			host = after
		}
	} else {
		u, err := url.Parse(remote)
		if err != nil {
			return "", "", ""
		}
		host, path = u.Hostname(), u.Path
		if u.Scheme == "http" || u.Scheme == "https" {
			web = u.Scheme + "://" + strings.ToLower(u.Host)
		}
	}
	host = strings.ToLower(host)
	if web == "" {
		web = "https://" + host
	}
	return host, web, strings.TrimSuffix(strings.Trim(path, "/"), ".git")
}

// gh resolves the repo itself, as it always has. Both CLIs list
// newest-created first; ask for recently updated so the limit cuts the right
// end. A later --search (review-requested) replaces this one.
func ghArgv(_ string, limit int) []string {
	return []string{
		"gh", "pr", "list",
		"--state", "open",
		"--search", "sort:updated-desc",
		"--limit", strconv.Itoa(limit),
		"--json", "number,title,author,isDraft,updatedAt",
	}
}

func parseGH(data []byte) ([]PR, error) {
	var raw []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		IsDraft        bool       `json:"isDraft"`
		UpdatedAt      time.Time  `json:"updatedAt"`
		URL            string     `json:"url"`
		HeadRefOid     string     `json:"headRefOid"`
		ReviewDecision string     `json:"reviewDecision"`
		ReviewRequests []struct{} `json:"reviewRequests"`
		// A pointer tells "not asked for" (nil) from "no checks" (empty).
		StatusCheckRollup *[]ghCheck `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	prs := make([]PR, 0, len(raw))
	for _, r := range raw {
		prs = append(prs, PR{
			Number:  r.Number,
			Title:   r.Title,
			Author:  r.Author.Login,
			Draft:   r.IsDraft,
			Updated: r.UpdatedAt,
			URL:     r.URL,
			SHA:     r.HeadRefOid,
			Review:  ghReview(r.ReviewDecision, len(r.ReviewRequests)),
			CI:      ghCI(r.StatusCheckRollup),
		})
	}
	return prs, nil
}

// ghCheck is a CheckRun (status, conclusion) or a StatusContext (state).
type ghCheck struct {
	Status, Conclusion, State string
}

func ghReview(decision string, requests int) Review {
	switch {
	case decision == "APPROVED":
		return Approved
	case decision == "CHANGES_REQUESTED":
		return ChangesRequested
	case decision == "REVIEW_REQUIRED" || requests > 0:
		return ReviewRequested
	}
	return ""
}

func ghCI(rollup *[]ghCheck) CI {
	if rollup == nil {
		return ""
	}
	if len(*rollup) == 0 {
		return CINone
	}
	ci := CIPassing
	for _, c := range *rollup {
		switch c.State + c.Conclusion { // one of the two is always empty
		case "FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
			return CIFailing
		}
		if c.State == "PENDING" || c.State == "EXPECTED" || (c.State == "" && c.Status != "COMPLETED") {
			ci = CIPending
		}
	}
	return ci
}

// glab mr list defaults to open MRs. --repo pins the project so glab neither
// guesses between remotes (forks) nor falls back to gitlab.com.
func glabArgv(project string, limit int) []string {
	return []string{"glab", "mr", "list", "--repo", project, "--order", "updated_at", "--sort", "desc",
		"--per-page", strconv.Itoa(limit), "--output", "json"}
}

func parseGLab(data []byte) ([]PR, error) {
	var raw []struct {
		IID    int    `json:"iid"`
		Title  string `json:"title"`
		Author struct {
			Username string `json:"username"`
		} `json:"author"`
		Draft     bool      `json:"draft"`
		WIP       bool      `json:"work_in_progress"` // pre-draft GitLab
		UpdatedAt time.Time `json:"updated_at"`
		WebURL    string    `json:"web_url"`
		SHA       string    `json:"sha"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	prs := make([]PR, 0, len(raw))
	for _, r := range raw {
		prs = append(prs, PR{
			Number:  r.IID,
			Title:   r.Title,
			Author:  r.Author.Username,
			Draft:   r.Draft || r.WIP,
			Updated: r.UpdatedAt,
			URL:     r.WebURL,
			SHA:     r.SHA,
		})
	}
	return prs, nil
}

// glabDetails fills in review and CI state with one GraphQL call: the REST
// list carries neither approvals nor the head pipeline.
func glabDetails(ctx context.Context, dir, web, path string, prs []PR) Status {
	iids := make([]string, len(prs))
	for i, pr := range prs {
		iids[i] = strconv.Quote(strconv.Itoa(pr.Number))
	}
	query := `query($p: ID!) { project(fullPath: $p) { mergeRequests(iids: [` + strings.Join(iids, ",") +
		`]) { nodes { iid approved approvedBy { nodes { username } } headPipeline { status }` +
		` reviewers { nodes { mergeRequestInteraction { reviewState } } } } } } }`
	_, hostport, _ := strings.Cut(web, "://") // same host and project as the listing
	out, err := run(ctx, dir, []string{"glab", "api", "--hostname", hostport, "graphql", "-f", "query=" + query, "-f", "p=" + path})
	if s := classify(ctx, err); s != OK {
		return s
	}
	details, err := parseGLabDetails(out)
	if err != nil {
		return Failed
	}
	for i := range prs {
		if d, ok := details[prs[i].Number]; ok {
			prs[i].Review, prs[i].CI = d.Review, d.CI
		}
	}
	return OK
}

func parseGLabDetails(data []byte) (map[int]PR, error) {
	var raw struct {
		Data struct {
			Project *struct {
				MergeRequests struct {
					Nodes []struct {
						IID        string `json:"iid"`
						Approved   bool   `json:"approved"`
						ApprovedBy struct {
							Nodes []struct{} `json:"nodes"`
						} `json:"approvedBy"`
						HeadPipeline *struct {
							Status string `json:"status"`
						} `json:"headPipeline"`
						Reviewers struct {
							Nodes []struct {
								Interaction *struct {
									ReviewState string `json:"reviewState"`
								} `json:"mergeRequestInteraction"`
							} `json:"nodes"`
						} `json:"reviewers"`
					} `json:"nodes"`
				} `json:"mergeRequests"`
			} `json:"project"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if len(raw.Errors) > 0 || raw.Data.Project == nil {
		return nil, errors.New("graphql: no project data")
	}
	out := map[int]PR{}
	for _, n := range raw.Data.Project.MergeRequests.Nodes {
		iid, err := strconv.Atoi(n.IID)
		if err != nil {
			return nil, err
		}
		var states []string
		for _, r := range n.Reviewers.Nodes {
			if r.Interaction != nil {
				states = append(states, r.Interaction.ReviewState)
			}
		}
		pipeline := ""
		if n.HeadPipeline != nil {
			pipeline = n.HeadPipeline.Status
		}
		out[iid] = PR{Review: glabReview(n.Approved, len(n.ApprovedBy.Nodes), states), CI: glabCI(pipeline)}
	}
	return out, nil
}

// glabReview: approved is only trusted with an approver, since a project with
// no required approvals reports every MR as approved.
func glabReview(approved bool, approvers int, states []string) Review {
	for _, s := range states {
		if s == "REQUESTED_CHANGES" {
			return ChangesRequested
		}
	}
	switch {
	case approved && approvers > 0:
		return Approved
	case !approved:
		return ReviewRequested
	}
	for _, s := range states {
		if s == "UNREVIEWED" || s == "REVIEW_STARTED" {
			return ReviewRequested
		}
	}
	return ""
}

func glabCI(pipeline string) CI {
	switch pipeline {
	case "", "SKIPPED":
		return CINone
	case "SUCCESS":
		return CIPassing
	case "FAILED", "CANCELED", "CANCELING":
		return CIFailing
	}
	return CIPending
}
