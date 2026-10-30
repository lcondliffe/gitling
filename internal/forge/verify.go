package forge

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os/exec"
	"strings"
)

// Request is a pull/merge request, in any state, as tidy needs to verify it.
type Request struct {
	State       string // "merged", "closed" or "open"
	HeadSHA     string
	Base        string
	MergeCommit string // merge or squash commit; empty when unknown
	CrossRepo   bool
}

// Requests looks up every PR/MR whose source branch is branch, read-only. Any
// failure is an error, so the caller can tell "no PRs" from "couldn't ask".
func Requests(dir, remoteURL, branch string) ([]Request, error) {
	var argv []string
	var parse func([]byte) ([]Request, error)
	host, web, path := parseRemote(remoteURL)
	switch kind := hosts[host]; {
	case path == "":
		return nil, errors.New("unsupported forge")
	case kind == "github":
		argv = []string{"gh", "pr", "list", "--head", branch, "--state", "all", "--limit", "50",
			"--json", "state,headRefOid,baseRefName,mergeCommit,isCrossRepository"}
		parse = parseGHRequests
	case kind == "gitlab":
		// Same host and project as List, not glab's guess from the remotes.
		_, hostport, _ := strings.Cut(web, "://")
		argv = []string{"glab", "api", "--hostname", hostport,
			"projects/" + url.PathEscape(path) + "/merge_requests?state=all&per_page=50&source_branch=" + url.QueryEscape(branch)}
		parse = parseGLRequests
	default:
		return nil, errors.New("unsupported forge")
	}
	ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parse(out)
}

func parseGHRequests(data []byte) ([]Request, error) {
	var raw []struct {
		State       string `json:"state"`
		HeadRefOid  string `json:"headRefOid"`
		BaseRefName string `json:"baseRefName"`
		MergeCommit *struct {
			Oid string `json:"oid"`
		} `json:"mergeCommit"`
		IsCrossRepository bool `json:"isCrossRepository"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make([]Request, 0, len(raw))
	for _, r := range raw {
		req := Request{
			State:     strings.ToLower(r.State),
			HeadSHA:   r.HeadRefOid,
			Base:      r.BaseRefName,
			CrossRepo: r.IsCrossRepository,
		}
		if r.MergeCommit != nil {
			req.MergeCommit = r.MergeCommit.Oid
		}
		out = append(out, req)
	}
	return out, nil
}

func parseGLRequests(data []byte) ([]Request, error) {
	var raw []struct {
		State           string `json:"state"`
		SHA             string `json:"sha"`
		TargetBranch    string `json:"target_branch"`
		MergeCommitSHA  string `json:"merge_commit_sha"`
		SquashCommitSHA string `json:"squash_commit_sha"`
		SourceProject   int    `json:"source_project_id"`
		TargetProject   int    `json:"target_project_id"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make([]Request, 0, len(raw))
	for _, r := range raw {
		state := r.State
		if state == "opened" || state == "locked" {
			state = "open"
		}
		commit := r.MergeCommitSHA
		if commit == "" {
			commit = r.SquashCommitSHA
		}
		out = append(out, Request{
			State:       state,
			HeadSHA:     r.SHA,
			Base:        r.TargetBranch,
			MergeCommit: commit,
			CrossRepo:   r.SourceProject != r.TargetProject,
		})
	}
	return out, nil
}
