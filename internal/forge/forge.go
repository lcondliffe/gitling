// Package forge lists the open pull/merge requests for a repository by
// shelling out to the hosting platform's own CLI (`gh` for GitHub, and
// whatever comes next). gitling never talks to a forge API itself, so it never
// handles tokens: the CLI is already installed and authenticated, or the panel
// simply doesn't appear.
package forge

import (
	"context"
	"encoding/json"
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

// listTimeout bounds the CLI call. The dashboard is worth waiting a moment
// for, not worth hanging on.
const listTimeout = 3 * time.Second

// forges maps a host in the remote URL to the CLI that lists its open PRs.
// Supporting another platform is one more entry: the host to match, the
// command to run, and a decoder for that command's JSON (e.g. GitLab's
// `glab mr list -F json`, Azure DevOps' `az repos pr list -o json`).
var forges = []struct {
	host  string
	argv  func(limit int) []string
	parse func([]byte) ([]PR, error)
}{
	{"github.com", ghArgv, parseGH},
}

// List returns the open PRs for the repo in dir, whose origin remote is
// remoteURL, newest first and capped at limit.
//
// Everything that can go wrong here — an unrecognised host, a missing or
// unauthenticated CLI, no network, a timeout — yields no PRs and no error. The
// panel is a bonus; a dashboard that failed because `gh` wasn't installed
// would be a worse tool.
func List(dir, remoteURL string, limit int) []PR {
	host := remoteHost(remoteURL)
	for _, f := range forges {
		if host != f.host {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
		defer cancel()
		argv := f.argv(limit)
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			return nil
		}
		prs, err := f.parse(out)
		if err != nil {
			return nil
		}
		return prs
	}
	return nil
}

// remoteHost pulls the hostname out of a git remote, which comes in two
// shapes: a URL (https://host/p, ssh://git@host:22/p) and SCP shorthand
// (git@host:p). Matching has to be on the host itself — a substring test
// would take notgithub.com for github.com.
func remoteHost(remote string) string {
	remote = strings.TrimSpace(remote)
	if !strings.Contains(remote, "://") {
		// SCP shorthand: everything before the first colon, minus any user@.
		host, _, ok := strings.Cut(remote, ":")
		if !ok {
			return ""
		}
		if _, after, ok := strings.Cut(host, "@"); ok {
			host = after
		}
		return strings.ToLower(host)
	}
	u, err := url.Parse(remote)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func ghArgv(limit int) []string {
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
