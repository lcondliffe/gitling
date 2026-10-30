package forge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGHRequests(t *testing.T) {
	got, err := parseGHRequests([]byte(`[
	  {"state":"MERGED","headRefOid":"aaa","baseRefName":"main","mergeCommit":{"oid":"mmm"},"isCrossRepository":false},
	  {"state":"CLOSED","headRefOid":"bbb","baseRefName":"main","mergeCommit":null,"isCrossRepository":true}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Request{
		{State: "merged", HeadSHA: "aaa", Base: "main", MergeCommit: "mmm"},
		{State: "closed", HeadSHA: "bbb", Base: "main", CrossRepo: true},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParseGLRequests(t *testing.T) {
	got, err := parseGLRequests([]byte(`[
	  {"state":"merged","sha":"aaa","target_branch":"main","merge_commit_sha":null,"squash_commit_sha":"sss","source_project_id":1,"target_project_id":1},
	  {"state":"opened","sha":"bbb","target_branch":"main","source_project_id":2,"target_project_id":1}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Request{
		{State: "merged", HeadSHA: "aaa", Base: "main", MergeCommit: "sss"},
		{State: "open", HeadSHA: "bbb", Base: "main", CrossRepo: true},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// An unknown host is an error, never an empty "no PRs" answer.
func TestRequestsUnsupportedHost(t *testing.T) {
	if _, err := Requests(t.TempDir(), "git@example.com:a/b.git", "x"); err == nil {
		t.Error("want an error for an unsupported forge")
	}
}

// A self-managed GitLab host is asked directly, for the remote's own project.
func TestRequestsSelfManagedGitLab(t *testing.T) {
	if err := AddHost("git.corp.example", "gitlab"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { delete(hosts, "git.corp.example") })
	args := filepath.Join(t.TempDir(), "args")
	stub(t, "glab", `echo "$@" > `+args+`; echo '[]'`)
	if _, err := Requests(t.TempDir(), "https://git.corp.example:8443/group/app.git", "feat/x"); err != nil {
		t.Fatal(err)
	}
	recorded, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	want := "api --hostname git.corp.example:8443 projects/group%2Fapp/merge_requests?state=all&per_page=50&source_branch=feat%2Fx"
	if got := strings.TrimSpace(string(recorded)); got != want {
		t.Errorf("argv %q, want %q", got, want)
	}
}
