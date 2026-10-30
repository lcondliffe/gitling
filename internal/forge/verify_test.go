package forge

import "testing"

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
