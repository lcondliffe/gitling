package forge

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseGH(t *testing.T) {
	data := []byte(`[
	  {"number":42,"title":"feat: add the widget","author":{"login":"ada"},"isDraft":false,"updatedAt":"2024-06-15T10:00:00Z"},
	  {"number":41,"title":"wip","author":{"login":"alan"},"isDraft":true,"updatedAt":"2024-06-14T09:00:00Z"}
	]`)

	prs, err := parseGH(data)
	if err != nil {
		t.Fatalf("parseGH: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("got %d PRs, want 2: %+v", len(prs), prs)
	}
	want := PR{
		Number:  42,
		Title:   "feat: add the widget",
		Author:  "ada",
		Updated: time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC),
	}
	if prs[0] != want {
		t.Errorf("prs[0] = %+v, want %+v", prs[0], want)
	}
	if !prs[1].Draft {
		t.Errorf("prs[1] should be a draft: %+v", prs[1])
	}
}

func TestParseGLab(t *testing.T) {
	data := []byte(`[
	  {"iid":7,"title":"feat: add the widget","author":{"username":"ada"},"draft":true,"updated_at":"2024-06-15T10:00:00.000Z"}
	]`)

	prs, err := parseGLab(data)
	if err != nil {
		t.Fatalf("parseGLab: %v", err)
	}
	want := PR{
		Number:  7,
		Title:   "feat: add the widget",
		Author:  "ada",
		Draft:   true,
		Updated: time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC),
	}
	if len(prs) != 1 || prs[0] != want {
		t.Errorf("got %+v, want [%+v]", prs, want)
	}
}

func TestParseGLabLegacyWIP(t *testing.T) {
	prs, err := parseGLab([]byte(`[{"iid":1,"work_in_progress":true}]`))
	if err != nil || len(prs) != 1 || !prs[0].Draft {
		t.Errorf("got %+v, %v; want one draft", prs, err)
	}
}

func TestParseRemote(t *testing.T) {
	for remote, want := range map[string][2]string{
		"https://github.com/acme/thing.git":               {"github.com", "acme/thing"},
		"https://user:tok@github.com/acme/thing":          {"github.com", "acme/thing"},
		"git@github.com:acme/thing.git":                   {"github.com", "acme/thing"},
		"ssh://git@github.com:22/acme/thing.git":          {"github.com", "acme/thing"},
		"git://GitHub.com/acme/thing.git":                 {"github.com", "acme/thing"},
		"https://notgithub.com/acme/thing.git":            {"notgithub.com", "acme/thing"},
		"git@gitlab.example.com:acme/thing.git":           {"gitlab.example.com", "acme/thing"},
		"git@gitlab.com:group/sub/deeper/thing.git":       {"gitlab.com", "group/sub/deeper/thing"},
		"ssh://git@git.corp.example:2222/group/sub/thing": {"git.corp.example", "group/sub/thing"},
		"https://git.corp.example/group/sub/thing.git/":   {"git.corp.example", "group/sub/thing"},
		"/srv/git/thing.git":                              {"", ""},
	} {
		if host, _, path := parseRemote(remote); host != want[0] || path != want[1] {
			t.Errorf("parseRemote(%q) = %q, %q, want %q, %q", remote, host, path, want[0], want[1])
		}
	}
}

// glab is always told which project to query: nested namespaces and SSH
// remotes become a web URL, a fork's own origin is used rather than glab's
// guess, and a configured self-managed host works like gitlab.com.
func TestGLabGetsExplicitProject(t *testing.T) {
	if err := AddHost("Git.Corp.Example", "gitlab"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { delete(hosts, "git.corp.example") })
	for remote, want := range map[string]string{
		"git@gitlab.com:group/sub/thing.git":               "--repo https://gitlab.com/group/sub/thing ",
		"https://gitlab.com/me/forked-thing.git":           "--repo https://gitlab.com/me/forked-thing ",
		"ssh://git@git.corp.example:2222/team/app/svc.git": "--repo https://git.corp.example/team/app/svc ",
		"https://git.corp.example:8443/group/project.git":  "--repo https://git.corp.example:8443/group/project ",
		"http://git.corp.example/group/project.git":        "--repo http://git.corp.example/group/project ",
	} {
		args := filepath.Join(t.TempDir(), "args")
		stub(t, "glab", `echo "$@" > `+args+`; echo '[]'`)
		if res := List(t.TempDir(), remote, 5, Options{}); res.CLI != "glab" || res.Status != OK {
			t.Errorf("%s: got %+v", remote, res)
		}
		recorded, err := os.ReadFile(args)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(recorded), want) {
			t.Errorf("%s: argv %q lacks %q", remote, recorded, want)
		}
	}
}

func TestAddHostRejectsUnknownKind(t *testing.T) {
	if err := AddHost("git.example", "bitbucket"); err == nil {
		t.Error("want an error for an unknown forge kind")
	}
}

// A remote no configured forge recognises never runs a command — including
// hosts that merely contain a known one.
func TestListSkipsUnknownHost(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // anything run would come back MissingCLI
	for _, remote := range []string{
		"",
		"git@bitbucket.org:acme/thing.git",
		"https://notgithub.com/acme/thing.git",
		"https://github.com.evil.example/acme/thing.git",
	} {
		res := List(".", remote, 5, Options{})
		if res.Status != Unsupported || res.Items != nil || res.CLI != "" {
			t.Errorf("%q: got %+v, want unsupported", remote, res)
		}
	}
}

// stub puts an executable named cli on PATH for the duration of the test, and
// nothing else: the shell script body decides what the fake CLI does. PATH
// holds only that directory, so the body has to stick to shell builtins.
func stub(t *testing.T, cli, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stub isn't executable on Windows")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, cli), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// adapters pairs each forge with a remote it serves and a canned JSON list of
// three items, so every state is exercised against both CLIs.
var adapters = []struct {
	cli, remote, limitFlag, three string
}{
	{"gh", "https://github.com/acme/thing.git", "--limit", `[
	  {"number":3,"title":"c","author":{"login":"ada"},"updatedAt":"2024-06-15T10:00:00Z"},
	  {"number":2,"title":"b","author":{"login":"ada"},"updatedAt":"2024-06-14T10:00:00Z"},
	  {"number":1,"title":"a","author":{"login":"ada"},"updatedAt":"2024-06-13T10:00:00Z"}]`},
	{"glab", "git@gitlab.com:acme/thing.git", "--per-page", `[
	  {"iid":3,"title":"c","author":{"username":"ada"},"updated_at":"2024-06-15T10:00:00Z"},
	  {"iid":2,"title":"b","author":{"username":"ada"},"updated_at":"2024-06-14T10:00:00Z"},
	  {"iid":1,"title":"a","author":{"username":"ada"},"updated_at":"2024-06-13T10:00:00Z"}]`},
}

func TestListStates(t *testing.T) {
	for _, a := range adapters {
		for _, tc := range []struct {
			name      string
			body      string // empty == no CLI on PATH at all
			limit     int
			status    Status
			items     int
			truncated bool
		}{
			{name: "empty", body: `echo '[]'`, limit: 5, status: OK},
			{name: "fits", body: "echo '" + a.three + "'", limit: 3, status: OK, items: 3},
			{name: "truncated", body: "echo '" + a.three + "'", limit: 2, status: OK, items: 2, truncated: true},
			{name: "missing", limit: 5, status: MissingCLI},
			{name: "exit 4", body: `exit 4`, limit: 5, status: Unauthenticated},
			{name: "auth stderr", body: `echo "To get started, run: ` + a.cli + ` auth login" >&2; exit 1`, limit: 5, status: Unauthenticated},
			{name: "timeout", body: `while :; do :; done`, limit: 5, status: Timeout},
			{name: "exit failure", body: `echo "HTTP 502" >&2; exit 1`, limit: 5, status: Failed},
			{name: "garbage output", body: `echo "not json"`, limit: 5, status: Failed},
		} {
			t.Run(a.cli+"/"+tc.name, func(t *testing.T) {
				if tc.body == "" {
					t.Setenv("PATH", t.TempDir()) // empty dir: nothing to find
				} else {
					stub(t, a.cli, tc.body)
				}
				if tc.status == Timeout {
					old := listTimeout
					listTimeout = 100 * time.Millisecond
					t.Cleanup(func() { listTimeout = old })
				}
				res := List(t.TempDir(), a.remote, tc.limit, Options{})
				if res.CLI != a.cli || res.Status != tc.status || len(res.Items) != tc.items || res.Truncated != tc.truncated {
					t.Errorf("got %+v, want cli %s status %s, %d items, truncated %v",
						res, a.cli, tc.status, tc.items, tc.truncated)
				}
				if res.CheckedAt.IsZero() {
					t.Error("CheckedAt not set")
				}
				if tc.status != OK && res.Note() == "" {
					t.Errorf("status %s has no note", res.Status)
				}
			})
		}
	}
}

// The CLI is asked for one more than the limit, which is how truncation is seen.
func TestListRequestsOneExtra(t *testing.T) {
	for _, a := range adapters {
		t.Run(a.cli, func(t *testing.T) {
			args := filepath.Join(t.TempDir(), "args")
			stub(t, a.cli, `echo "$@" > `+args+`; echo '[]'`)
			List(t.TempDir(), a.remote, 5, Options{})
			recorded, err := os.ReadFile(args)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(recorded), a.limitFlag+" 6") {
				t.Errorf("want %s 6 passed to the CLI: %q", a.limitFlag, recorded)
			}
		})
	}
}

func TestParseGHDetails(t *testing.T) {
	data := []byte(`[
	  {"number":5,"reviewDecision":"APPROVED","statusCheckRollup":[{"status":"COMPLETED","conclusion":"SUCCESS"},{"state":"SUCCESS"}]},
	  {"number":4,"reviewDecision":"CHANGES_REQUESTED","statusCheckRollup":[{"status":"COMPLETED","conclusion":"SUCCESS"},{"state":"FAILURE"}]},
	  {"number":3,"reviewRequests":[{"login":"ada"}],"statusCheckRollup":[{"status":"IN_PROGRESS","conclusion":""}]},
	  {"number":2,"reviewDecision":"REVIEW_REQUIRED","statusCheckRollup":[]},
	  {"number":1,"url":"https://github.com/acme/thing/pull/1","headRefOid":"abc"}
	]`)
	prs, err := parseGH(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		review Review
		ci     CI
	}{
		{Approved, CIPassing},
		{ChangesRequested, CIFailing},
		{ReviewRequested, CIPending},
		{ReviewRequested, CINone},
		{"", ""}, // rollup not asked for: unknown, not "no checks"
	}
	for i, w := range want {
		if prs[i].Review != w.review || prs[i].CI != w.ci {
			t.Errorf("#%d: got %s/%s, want %s/%s", prs[i].Number, prs[i].Review, prs[i].CI, w.review, w.ci)
		}
	}
	if prs[4].URL != "https://github.com/acme/thing/pull/1" || prs[4].SHA != "abc" {
		t.Errorf("url/sha not parsed: %+v", prs[4])
	}
}

const glabGraphQL = `{"data":{"project":{"mergeRequests":{"nodes":[
  {"iid":"3","approved":true,"approvedBy":{"nodes":[{"username":"bob"}]},"headPipeline":{"status":"SUCCESS"},"reviewers":{"nodes":[{"mergeRequestInteraction":{"reviewState":"APPROVED"}}]}},
  {"iid":"2","approved":true,"approvedBy":{"nodes":[]},"headPipeline":null,"reviewers":{"nodes":[{"mergeRequestInteraction":{"reviewState":"REQUESTED_CHANGES"}}]}},
  {"iid":"1","approved":false,"approvedBy":{"nodes":[]},"headPipeline":{"status":"RUNNING"},"reviewers":{"nodes":[]}}
]}}}}`

func TestParseGLabDetails(t *testing.T) {
	got, err := parseGLabDetails([]byte(glabGraphQL))
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]PR{
		3: {Review: Approved, CI: CIPassing},
		2: {Review: ChangesRequested, CI: CINone},
		1: {Review: ReviewRequested, CI: CIPending},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for iid, w := range want {
		if got[iid] != w {
			t.Errorf("!%d: got %+v, want %+v", iid, got[iid], w)
		}
	}
	// Approved with nobody approving is just "no approvals required".
	if r := glabReview(true, 0, nil); r != "" {
		t.Errorf("glabReview(true, 0) = %q, want none", r)
	}
	if _, err := parseGLabDetails([]byte(`{"data":{"project":null},"errors":[{"message":"nope"}]}`)); err == nil {
		t.Error("a GraphQL error should fail")
	}
}

// The GraphQL enrichment asks the same host and project as the listing.
func TestGLabDetailsTargetsListedProject(t *testing.T) {
	if err := AddHost("git.corp.example", "gitlab"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { delete(hosts, "git.corp.example") })
	args := filepath.Join(t.TempDir(), "args")
	list := `[{"iid":3,"title":"c","author":{"username":"ada"},"updated_at":"2024-06-15T10:00:00Z"}]`
	stub(t, "glab", `echo "$@" >> `+args+`; if [ "$1" = api ]; then echo '`+glabGraphQL+`'; else echo '`+list+`'; fi`)
	res := List(t.TempDir(), "https://git.corp.example:8443/grp/sub/proj.git", 5, Options{Details: true})
	if res.Details != OK {
		t.Fatalf("got %+v", res)
	}
	recorded, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(recorded)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "--repo https://git.corp.example:8443/grp/sub/proj ") ||
		!strings.HasPrefix(lines[1], "api --hostname git.corp.example:8443 graphql") || !strings.HasSuffix(lines[1], "p=grp/sub/proj") {
		t.Errorf("argv %q", lines)
	}
}

// glab gets review/CI state from a second call; its failure keeps the list
// and says so.
func TestListGLabDetails(t *testing.T) {
	list := `[{"iid":3,"title":"c","author":{"username":"ada"},"updated_at":"2024-06-15T10:00:00Z","web_url":"https://gitlab.com/acme/thing/-/merge_requests/3"}]`
	for _, tc := range []struct {
		name, api string
		details   Status
		review    Review
	}{
		{"ok", "echo '" + glabGraphQL + "'", OK, Approved},
		{"forbidden", `echo "HTTP 403" >&2; exit 1`, Failed, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub(t, "glab", `if [ "$1" = api ]; then `+tc.api+`; else echo '`+list+`'; fi`)
			res := List(t.TempDir(), "git@gitlab.com:acme/thing.git", 5, Options{Details: true})
			if res.Status != OK || len(res.Items) != 1 || res.Details != tc.details || res.Items[0].Review != tc.review {
				t.Errorf("got %+v", res)
			}
			if (tc.details != OK) != (res.DetailsNote() != "") {
				t.Errorf("DetailsNote = %q for %s", res.DetailsNote(), tc.details)
			}
		})
	}
}

// Every query asks the forge for recently updated first: the CLIs default to
// creation order, so an old PR updated today would fall past the limit.
func TestListSortsByUpdatedServerSide(t *testing.T) {
	for _, a := range []struct{ cli, remote, sort string }{
		{"gh", "https://github.com/acme/thing.git", "sort:updated-desc"},
		{"glab", "git@gitlab.com:acme/thing.git", "--order updated_at --sort desc"},
	} {
		for _, mine := range []bool{false, true} {
			args := filepath.Join(t.TempDir(), "args")
			stub(t, a.cli, `echo "$@" >> `+args+`; echo '[]'`)
			List(t.TempDir(), a.remote, 5, Options{Mine: mine})
			recorded, err := os.ReadFile(args)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(strings.TrimSpace(string(recorded)), "\n") {
				if !strings.Contains(line, a.sort) {
					t.Errorf("%s mine=%v: query without %q: %q", a.cli, mine, a.sort, line)
				}
			}
		}
	}
}

// gh gets review/CI state in the same call, from extra --json fields.
func TestListGHDetailsArgs(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	stub(t, "gh", `echo "$@" > `+args+`; echo '[]'`)
	List(t.TempDir(), "https://github.com/acme/thing.git", 5, Options{Details: true})
	recorded, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(recorded), "statusCheckRollup") {
		t.Errorf("want review/CI fields requested: %q", recorded)
	}
}

// mine runs one query per personal scope, then dedupes and ranks before the
// limit.
func TestListMine(t *testing.T) {
	for _, a := range []struct{ cli, remote, authored, awaiting string }{
		{"gh", "https://github.com/acme/thing.git",
			`[{"number":3,"updatedAt":"2024-06-13T10:00:00Z"},{"number":2,"updatedAt":"2024-06-14T10:00:00Z"}]`,
			`[{"number":2,"updatedAt":"2024-06-14T10:00:00Z"},{"number":9,"updatedAt":"2024-06-15T10:00:00Z"}]`},
		{"glab", "git@gitlab.com:acme/thing.git",
			`[{"iid":3,"updated_at":"2024-06-13T10:00:00Z"},{"iid":2,"updated_at":"2024-06-14T10:00:00Z"}]`,
			`[{"iid":2,"updated_at":"2024-06-14T10:00:00Z"},{"iid":9,"updated_at":"2024-06-15T10:00:00Z"}]`},
	} {
		t.Run(a.cli, func(t *testing.T) {
			stub(t, a.cli, `case "$*" in *--author*) echo '`+a.authored+`';; *) echo '`+a.awaiting+`';; esac`)
			res := List(t.TempDir(), a.remote, 2, Options{Mine: true})
			var got []int
			for _, pr := range res.Items {
				got = append(got, pr.Number)
			}
			if res.Status != OK || !res.Truncated || len(got) != 2 || got[0] != 9 || got[1] != 2 {
				t.Errorf("got %v truncated %v status %s, want [9 2] truncated", got, res.Truncated, res.Status)
			}
		})
	}
}
