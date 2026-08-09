package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/lcondliffe/gitling/internal/gitdata"
	"github.com/lcondliffe/gitling/internal/tidy"
)

// Tests for the half of tidy that can delete something. They drive tidyRun
// through the tidyRepo seam with a fake, so the confirmation gate and the
// delete loop are exercised without a repository to lose branches from.

// deletion is one call the fake received, so a test can assert not just which
// branches were deleted but whether -d or -D was used for each.
type deletion struct {
	name  string
	force bool
}

// fakeRepo is a scripted tidyRepo that records what tidy asked it to do.
type fakeRepo struct {
	branches   []gitdata.Branch
	base       string
	fetchErr   error
	branchErr  error
	deleteErrs map[string]error // by branch name; a branch git would refuse

	fetched int
	pruned  bool // whether the last fetch asked for a prune
	deleted []deletion
}

func (f *fakeRepo) Branches() ([]gitdata.Branch, error) {
	if f.branchErr != nil {
		return nil, f.branchErr
	}
	return f.branches, nil
}

func (f *fakeRepo) DefaultBranch() string { return f.base }

func (f *fakeRepo) Fetch(prune bool) error {
	f.fetched++
	f.pruned = prune
	return f.fetchErr
}

func (f *fakeRepo) DeleteBranch(name string, force bool) error {
	f.deleted = append(f.deleted, deletion{name: name, force: force})
	return f.deleteErrs[name]
}

// names is the branches the fake was asked to delete, in order.
func (f *fakeRepo) names() []string {
	var out []string
	for _, d := range f.deleted {
		out = append(out, d.name)
	}
	return out
}

// newFakeRepo returns a repository with one of each interesting shape: the
// default branch (checked out, never a candidate), a merged branch that -d
// would accept, a gone branch that needs -D, and an active branch that is not a
// candidate at all.
func newFakeRepo() *fakeRepo {
	now := time.Now()
	return &fakeRepo{
		base: "origin/main",
		branches: []gitdata.Branch{
			{Name: "main", IsHead: true, Merged: true, Tip: "aaa1111", LastCommit: now},
			{Name: "feat/merged", Merged: true, Tip: "bbb2222", LastCommit: now.Add(-48 * time.Hour)},
			{Name: "feat/gone", Gone: true, Upstream: "origin/feat/gone", Tip: "ccc3333", LastCommit: now.Add(-24 * time.Hour)},
			{Name: "feat/active", Tip: "ddd4444", LastCommit: now},
		},
	}
}

// applyOpts is what `gitling tidy --apply` resolves to, prompt included.
func applyOpts() tidyOptions {
	return tidyOptions{apply: true, fetch: true}
}

func runTidyWith(t *testing.T, repo tidyRepo, stdin string, o tidyOptions) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err = tidyRun(&out, &errOut, strings.NewReader(stdin), repo, o)
	return out.String(), errOut.String(), err
}

func TestTidyRunDryRunDeletesNothing(t *testing.T) {
	repo := newFakeRepo()
	out, _, err := runTidyWith(t, repo, "y\n", tidyOptions{fetch: true})
	if err != nil {
		t.Fatalf("tidyRun() error: %v", err)
	}
	if len(repo.deleted) != 0 {
		t.Errorf("dry run deleted %v, want nothing", repo.names())
	}
	if !strings.Contains(out, "would be deleted") {
		t.Errorf("dry-run output missing the would-be-deleted footer:\n%s", out)
	}
	for _, name := range []string{"feat/merged", "feat/gone"} {
		if !strings.Contains(out, name) {
			t.Errorf("dry-run output missing candidate %q:\n%s", name, out)
		}
	}
	// The recovery hashes are the reason -D is allowed at all.
	if !strings.Contains(out, "ccc3333") {
		t.Errorf("dry-run output missing the tip hash to restore from:\n%s", out)
	}
}

// TestTidyRunConfirmGate is the whole point of the seam: --apply deletes only
// on an explicit yes, and every other answer shape leaves the branches alone.
func TestTidyRunConfirmGate(t *testing.T) {
	tests := []struct {
		name       string
		stdin      string
		wantDelete bool
	}{
		{name: "lowercase y", stdin: "y\n", wantDelete: true},
		{name: "uppercase Y", stdin: "Y\n", wantDelete: true},
		{name: "yes", stdin: "yes\n", wantDelete: true},
		{name: "YES", stdin: "YES\n", wantDelete: true},
		{name: "padded with whitespace", stdin: "  y  \n", wantDelete: true},
		{name: "no trailing newline", stdin: "y", wantDelete: true},
		{name: "bare newline", stdin: "\n"},
		{name: "n", stdin: "n\n"},
		{name: "N", stdin: "N\n"},
		{name: "no", stdin: "no\n"},
		{name: "empty stdin", stdin: ""},
		{name: "anything else", stdin: "maybe\n"},
		{name: "a word starting with y", stdin: "yeah\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeRepo()
			out, _, err := runTidyWith(t, repo, tt.stdin, applyOpts())
			if err != nil {
				t.Fatalf("tidyRun() error: %v", err)
			}
			if got := len(repo.deleted) > 0; got != tt.wantDelete {
				t.Fatalf("deleted %v, want deletions = %v", repo.names(), tt.wantDelete)
			}
			if !tt.wantDelete && !strings.Contains(out, "aborted; nothing was deleted") {
				t.Errorf("declined run did not say nothing was deleted:\n%s", out)
			}
			if !strings.Contains(out, "[y/N]") {
				t.Errorf("output missing the confirmation prompt:\n%s", out)
			}
		})
	}
}

// TestTidyRunAppliesForcePerCandidate pins the -d/-D split: only a branch git
// itself verified as merged is deleted without force.
func TestTidyRunAppliesForcePerCandidate(t *testing.T) {
	repo := newFakeRepo()
	out, _, err := runTidyWith(t, repo, "y\n", applyOpts())
	if err != nil {
		t.Fatalf("tidyRun() error: %v", err)
	}
	want := []deletion{
		{name: "feat/merged", force: false},
		{name: "feat/gone", force: true},
	}
	if len(repo.deleted) != len(want) {
		t.Fatalf("deleted %v, want %v", repo.deleted, want)
	}
	for i, w := range want {
		if repo.deleted[i] != w {
			t.Errorf("deletion %d = %+v, want %+v", i, repo.deleted[i], w)
		}
	}
	if !strings.Contains(out, "2 branches deleted") {
		t.Errorf("applied output missing the deleted count:\n%s", out)
	}
}

// The plan is printed once, before the prompt; what follows the answer is the
// outcome, not the same table again.
func TestTidyRunConfirmedApplyPrintsThePlanOnce(t *testing.T) {
	repo := newFakeRepo()
	repo.deleteErrs = map[string]error{"feat/gone": errors.New("not fully merged")}

	out, _, err := runTidyWith(t, repo, "y\n", applyOpts())
	if err == nil {
		t.Fatal("tidyRun() = nil error, want the refusal reported")
	}
	if n := strings.Count(out, "feat/merged"); n != 1 {
		t.Errorf("branch listed %d times, want 1:\n%s", n, out)
	}
	if n := strings.Count(out, "TIDY"); n != 1 {
		t.Errorf("header printed %d times, want 1:\n%s", n, out)
	}
	for _, want := range []string{"1 branch deleted", "kept feat/gone: not fully merged"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestTidyRunYesSkipsThePrompt(t *testing.T) {
	repo := newFakeRepo()
	o := applyOpts()
	o.assumeYes = true
	out, _, err := runTidyWith(t, repo, "", o)
	if err != nil {
		t.Fatalf("tidyRun() error: %v", err)
	}
	if len(repo.deleted) != 2 {
		t.Errorf("deleted %v, want both candidates", repo.names())
	}
	if strings.Contains(out, "[y/N]") {
		t.Errorf("--yes still prompted:\n%s", out)
	}
}

// TestTidyRunPartialDeleteFailure: one branch git refuses must not stop the
// rest, and the failure has to be reported rather than swallowed.
func TestTidyRunPartialDeleteFailure(t *testing.T) {
	repo := newFakeRepo()
	repo.deleteErrs = map[string]error{"feat/merged": errors.New("not fully merged")}
	o := applyOpts()
	o.assumeYes = true

	out, _, err := runTidyWith(t, repo, "", o)
	if err == nil {
		t.Fatal("tidyRun() = nil error, want a failure report")
	}
	if want := "1 branch could not be deleted"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if got := repo.names(); len(got) != 2 {
		t.Errorf("attempted %v, want the refused branch not to stop the rest", got)
	}
	if !strings.Contains(out, "1 branch deleted") {
		t.Errorf("output missing the surviving deletion:\n%s", out)
	}
	if !strings.Contains(out, "not fully merged") {
		t.Errorf("output missing the failure reason:\n%s", out)
	}
}

// TestTidyRunFetchFailureDegrades: offline is a warning, not an abort — but it
// must be loud, because the plan below it is built on stale information.
func TestTidyRunFetchFailureDegrades(t *testing.T) {
	repo := newFakeRepo()
	repo.fetchErr = errors.New("could not resolve host")
	o := applyOpts()
	o.assumeYes = true

	out, errOut, err := runTidyWith(t, repo, "", o)
	if err != nil {
		t.Fatalf("tidyRun() error: %v", err)
	}
	if repo.fetched != 1 {
		t.Errorf("fetched %d times, want 1", repo.fetched)
	}
	for _, want := range []string{"fetch failed", "could not resolve host", "may not show as gone"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr missing %q:\n%s", want, errOut)
		}
	}
	if len(repo.deleted) != 2 {
		t.Errorf("deleted %v, want the run to continue from local refs", repo.names())
	}
	if strings.Contains(out, "fetch failed") {
		t.Errorf("the warning belongs on stderr, not in the plan:\n%s", out)
	}
}

// The fetch has to prune: Gone is derived from remote-tracking refs, so without
// pruning a branch deleted on the forge still looks alive and the most useful
// category comes back empty.
func TestTidyRunFetchPrunes(t *testing.T) {
	repo := newFakeRepo()
	if _, _, err := runTidyWith(t, repo, "", tidyOptions{fetch: true}); err != nil {
		t.Fatalf("tidyRun() error: %v", err)
	}
	if repo.fetched != 1 {
		t.Fatalf("fetched %d times, want 1", repo.fetched)
	}
	if !repo.pruned {
		t.Error("fetched without --prune, so gone-ness would be stale")
	}
}

func TestTidyRunNoFetchSkipsTheFetch(t *testing.T) {
	repo := newFakeRepo()
	if _, _, err := runTidyWith(t, repo, "", tidyOptions{}); err != nil {
		t.Fatalf("tidyRun() error: %v", err)
	}
	if repo.fetched != 0 {
		t.Errorf("fetched %d times with fetch off, want 0", repo.fetched)
	}
}

func TestTidyRunBranchesErrorIsFatal(t *testing.T) {
	repo := newFakeRepo()
	repo.branchErr = errors.New("not a git repository")
	o := applyOpts()
	o.assumeYes = true

	_, _, err := runTidyWith(t, repo, "", o)
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("tidyRun() error = %v, want the Branches failure", err)
	}
	if len(repo.deleted) != 0 {
		t.Errorf("deleted %v after failing to list branches, want nothing", repo.names())
	}
}

// TestTidyRunEmptyPlanNeverPrompts: with nothing to delete, --apply must not
// ask a question whose answer cannot matter.
func TestTidyRunEmptyPlanNeverPrompts(t *testing.T) {
	repo := newFakeRepo()
	repo.branches = []gitdata.Branch{
		{Name: "main", IsHead: true, Merged: true, Tip: "aaa1111", LastCommit: time.Now()},
		{Name: "feat/active", Tip: "ddd4444", LastCommit: time.Now()},
	}
	out, _, err := runTidyWith(t, repo, "y\n", applyOpts())
	if err != nil {
		t.Fatalf("tidyRun() error: %v", err)
	}
	if strings.Contains(out, "[y/N]") {
		t.Errorf("prompted with an empty plan:\n%s", out)
	}
	if len(repo.deleted) != 0 {
		t.Errorf("deleted %v from an empty plan", repo.names())
	}
}

// TestTidyRunProtectKeepsBranches proves the protect globs reach Classify from
// the resolved options rather than stopping at the flag.
func TestTidyRunProtectKeepsBranches(t *testing.T) {
	repo := newFakeRepo()
	o := applyOpts()
	o.assumeYes = true
	o.protect = []string{"feat/*"}

	out, _, err := runTidyWith(t, repo, "", o)
	if err != nil {
		t.Fatalf("tidyRun() error: %v", err)
	}
	if len(repo.deleted) != 0 {
		t.Errorf("deleted %v despite --protect feat/*", repo.names())
	}
	if !strings.Contains(out, "protected by feat/*") {
		t.Errorf("output does not say why the branches were kept:\n%s", out)
	}
}

// TestDeleteBranchesPartialFailure covers the loop on its own: every candidate
// is attempted, and only the failures come back.
func TestDeleteBranchesPartialFailure(t *testing.T) {
	refused := errors.New("not fully merged")
	repo := &fakeRepo{deleteErrs: map[string]error{"b": refused}}
	plan := tidy.Plan{Candidates: []tidy.Candidate{
		{Branch: gitdata.Branch{Name: "a"}, Reason: tidy.ReasonMerged},
		{Branch: gitdata.Branch{Name: "b"}, Reason: tidy.ReasonGone, Force: true},
		{Branch: gitdata.Branch{Name: "c"}, Reason: tidy.ReasonGone, Force: true},
	}}

	failed := deleteBranches(repo, plan)
	if len(failed) != 1 || !errors.Is(failed["b"], refused) {
		t.Fatalf("failed = %v, want only b refused", failed)
	}
	want := []string{"a", "b", "c"}
	got := repo.names()
	if len(got) != len(want) {
		t.Fatalf("attempted %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempted %v, want %v", got, want)
		}
	}
}

func TestDeleteBranchesAllSucceed(t *testing.T) {
	repo := &fakeRepo{}
	plan := tidy.Plan{Candidates: []tidy.Candidate{
		{Branch: gitdata.Branch{Name: "a"}, Reason: tidy.ReasonMerged},
	}}
	if failed := deleteBranches(repo, plan); failed != nil {
		t.Errorf("failed = %v, want nil when nothing was refused", failed)
	}
}

// TestConfirm checks the gate directly, including that the question is asked
// before the answer is read.
func TestConfirm(t *testing.T) {
	tests := []struct {
		name  string
		stdin string
		want  bool
	}{
		{name: "y", stdin: "y\n", want: true},
		{name: "yes with carriage return", stdin: "yes\r\n", want: true},
		{name: "leading whitespace", stdin: "\ty\n", want: true},
		{name: "newline", stdin: "\n"},
		{name: "n", stdin: "n\n"},
		{name: "eof", stdin: ""},
		{name: "y buried in a sentence", stdin: "why not\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			got := confirm(&out, strings.NewReader(tt.stdin), "Delete 2 branches?")
			if got != tt.want {
				t.Errorf("confirm(%q) = %v, want %v", tt.stdin, got, tt.want)
			}
			if !strings.Contains(out.String(), "Delete 2 branches? [y/N]") {
				t.Errorf("confirm did not ask the question:\n%s", out.String())
			}
		})
	}
}

// A closed or absent stdin is a no, and says so — an unattended run that meant
// to go through wants --yes, and should be told.
func TestConfirmWithNothingOnStdin(t *testing.T) {
	var out bytes.Buffer
	if confirm(&out, failingReader{}, "Delete 1 branch?") {
		t.Error("confirm with an unreadable stdin = true, want false")
	}
	if !strings.Contains(out.String(), "pass --yes") {
		t.Errorf("confirm did not explain how to run unattended:\n%s", out.String())
	}
}

// failingReader stands in for a stdin that cannot be read at all, as opposed to
// one that is simply empty.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, fmt.Errorf("stdin is closed") }

var _ io.Reader = failingReader{}

// The fake has to keep implementing the seam it exists to fill.
var _ tidyRepo = (*fakeRepo)(nil)

// The real repository has to satisfy it too, since runTidy passes one in.
var _ tidyRepo = (*gitdata.Repo)(nil)
