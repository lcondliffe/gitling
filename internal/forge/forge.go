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
	Truncated bool // more are open than Items holds
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

// listTimeout bounds the CLI call. The dashboard is worth waiting a moment
// for, not worth hanging on. A var so tests can shorten it.
var listTimeout = 3 * time.Second

type adapter struct {
	cli   string
	argv  func(project string, limit int) []string
	parse func([]byte) ([]PR, error)
}

// kinds are the supported forges, keyed by the name config uses for them.
var kinds = map[string]adapter{
	"github": {"gh", ghArgv, parseGH},
	"gitlab": {"glab", glabArgv, parseGLab},
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
// remoteURL, newest first and capped at limit.
//
// Nothing here is fatal: the panel is a bonus, so a missing or logged-out CLI,
// a timeout or bad output becomes a Status rather than an error.
func List(dir, remoteURL string, limit int) Result {
	res := Result{Status: Unsupported, CheckedAt: time.Now()}
	host, web, path := parseRemote(remoteURL)
	if f, ok := kinds[hosts[host]]; ok && path != "" {
		res.CLI = f.cli
		ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
		defer cancel()
		argv := f.argv(web+"/"+path, limit+1) // one extra reveals truncation
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = dir
		out, err := cmd.Output()
		if res.Status = classify(ctx, err); res.Status != OK {
			return res
		}
		prs, err := f.parse(out)
		if err != nil {
			res.Status = Failed
			return res
		}
		if len(prs) > limit {
			prs, res.Truncated = prs[:limit], true
		}
		res.Items = prs
		return res
	}
	return res
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

// gh resolves the repo itself, as it always has.
func ghArgv(_ string, limit int) []string {
	return []string{
		"gh", "pr", "list",
		"--state", "open",
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
		IsDraft   bool      `json:"isDraft"`
		UpdatedAt time.Time `json:"updatedAt"`
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
		})
	}
	return prs, nil
}

// glab mr list defaults to open MRs. --repo pins the project so glab neither
// guesses between remotes (forks) nor falls back to gitlab.com.
func glabArgv(project string, limit int) []string {
	return []string{"glab", "mr", "list", "--repo", project, "--per-page", strconv.Itoa(limit), "--output", "json"}
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
		})
	}
	return prs, nil
}
