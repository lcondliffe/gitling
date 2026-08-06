package tidy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lcondliffe/gitling/internal/gitdata"
)

// End-to-end over a real repository: git → gitdata.Branches → Classify.
// Skips when git is missing, like the gitdata integration tests.

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH; skipping integration test")
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gitAt(t, dir, time.Time{}, args...)
}

func gitAt(t *testing.T, dir string, when time.Time, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test Author",
		"GIT_AUTHOR_EMAIL=author@example.com",
		"GIT_COMMITTER_NAME=Test Author",
		"GIT_COMMITTER_EMAIL=author@example.com",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		// HOME alone leaves XDG_CONFIG_HOME in play; pin both config files.
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"HOME="+dir,
	)
	if !when.IsZero() {
		stamp := when.Format(time.RFC3339)
		cmd.Env = append(cmd.Env, "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, dir, name, content, msg string, when time.Time) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", name)
	gitAt(t, dir, when, "commit", "-q", "-m", msg)
}

// realRepo builds a repository containing every shape tidy classifies.
func realRepo(t *testing.T) (*gitdata.Repo, string) {
	t.Helper()
	requireGit(t)

	root := t.TempDir()
	local := filepath.Join(root, "local")
	remote := filepath.Join(root, "remote.git")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-q", "--bare", "-b", "main", remote)
	git(t, local, "init", "-q", "-b", "main")
	git(t, local, "config", "user.name", "Test Author")
	git(t, local, "config", "user.email", "author@example.com")
	commit(t, local, "README.md", "hello\n", "init", time.Time{})
	git(t, local, "remote", "add", "origin", remote)
	git(t, local, "push", "-q", "-u", "origin", "main")

	// merged: genuinely in main.
	git(t, local, "checkout", "-q", "-b", "merged-work")
	commit(t, local, "m.txt", "m\n", "merged work", time.Time{})
	git(t, local, "checkout", "-q", "main")
	git(t, local, "merge", "-q", "--no-ff", "-m", "merge merged-work", "merged-work")

	// gone: squash-merged, remote branch deleted behind our back.
	git(t, local, "checkout", "-q", "-b", "squashed-work")
	commit(t, local, "s.txt", "s\n", "squashed work", time.Time{})
	git(t, local, "push", "-q", "-u", "origin", "squashed-work")
	git(t, local, "checkout", "-q", "main")
	git(t, local, "merge", "-q", "--squash", "squashed-work")
	git(t, local, "commit", "-q", "-m", "squashed work (#1)")

	// "Merged" is measured against the remote default branch, so an unpushed
	// main would leave merged-work looking unmerged.
	git(t, local, "push", "-q", "origin", "main")
	git(t, remote, "branch", "-D", "squashed-work")

	// stale: old, unmerged, no upstream.
	old := time.Now().AddDate(0, 0, -400)
	git(t, local, "checkout", "-q", "-b", "old-spike")
	commit(t, local, "spike.txt", "spike\n", "old spike", old)

	// protected by glob: also stale, so it is only kept by the glob.
	git(t, local, "checkout", "-q", "main")
	git(t, local, "checkout", "-q", "-b", "release/1.0")
	commit(t, local, "r.txt", "r\n", "release prep", old)

	// fresh unmerged work: must never be a candidate.
	git(t, local, "checkout", "-q", "main")
	git(t, local, "checkout", "-q", "-b", "active-work")
	commit(t, local, "a.txt", "a\n", "active work", time.Time{})

	git(t, local, "checkout", "-q", "main")

	repo, err := gitdata.Open(local)
	if err != nil {
		t.Fatalf("gitdata.Open: %v", err)
	}
	if err := repo.Fetch(true); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	return repo, local
}

func classifyReal(t *testing.T, repo *gitdata.Repo, opts Options) Plan {
	t.Helper()
	branches, err := repo.Branches()
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	if opts.Base == "" {
		opts.Base = repo.DefaultBranch()
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	return Classify(branches, opts)
}

func candidateByName(p Plan, name string) (Candidate, bool) {
	for _, c := range p.Candidates {
		if c.Branch.Name == name {
			return c, true
		}
	}
	return Candidate{}, false
}

// Reason and force flag per real branch shape.
func TestRealRepoClassification(t *testing.T) {
	repo, _ := realRepo(t)
	plan := classifyReal(t, repo, Options{
		Reasons: map[Reason]bool{ReasonMerged: true, ReasonGone: true, ReasonStale: true},
		Protect: []string{"release/*"},
	})

	tests := []struct {
		branch     string
		wantReason Reason
		wantForce  bool
	}{
		{"merged-work", ReasonMerged, false},
		{"squashed-work", ReasonGone, true},
		{"old-spike", ReasonStale, true},
	}
	for _, tt := range tests {
		c, ok := candidateByName(plan, tt.branch)
		if !ok {
			t.Errorf("%s missing from the plan", tt.branch)
			continue
		}
		if c.Reason != tt.wantReason {
			t.Errorf("%s reason = %q, want %q", tt.branch, c.Reason, tt.wantReason)
		}
		if c.Force != tt.wantForce {
			t.Errorf("%s Force = %v, want %v", tt.branch, c.Force, tt.wantForce)
		}
	}

	for _, name := range []string{"main", "release/1.0", "active-work"} {
		if c, ok := candidateByName(plan, name); ok {
			t.Errorf("%s should not be a deletion candidate (got reason %q)", name, c.Reason)
		}
	}

	// Every candidate must carry the hash the restore instruction needs.
	for _, c := range plan.Candidates {
		if len(c.Branch.Tip) != 40 {
			t.Errorf("%s Tip = %q, want a full hash", c.Branch.Name, c.Branch.Tip)
		}
	}
}

// For every candidate, the force flag Classify chose must be the one git
// actually requires — checked by executing the plan for real.
func TestRealRepoPlanIsExecutable(t *testing.T) {
	repo, local := realRepo(t)
	plan := classifyReal(t, repo, Options{
		Reasons: map[Reason]bool{ReasonMerged: true, ReasonGone: true, ReasonStale: true},
		Protect: []string{"release/*"},
	})
	if plan.Empty() {
		t.Fatal("plan is empty; the fixture proves nothing")
	}

	for _, c := range plan.Candidates {
		tip := c.Branch.Tip
		if err := repo.DeleteBranch(c.Branch.Name, c.Force); err != nil {
			t.Errorf("deleting %s (reason %q, force %v) failed: %v", c.Branch.Name, c.Reason, c.Force, err)
			continue
		}
		// And it is recoverable from what the tool printed.
		git(t, local, "branch", c.Branch.Name, tip)
		if got := git(t, local, "rev-parse", c.Branch.Name); got != tip {
			t.Errorf("%s restored to %s, want %s", c.Branch.Name, got, tip)
		}
	}

	// The branches the plan kept must all still be there.
	for _, name := range []string{"main", "release/1.0", "active-work"} {
		cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
		cmd.Dir = local
		if cmd.Run() != nil {
			t.Errorf("%s was deleted but was never a candidate", name)
		}
	}
}

// A candidate marked Force=false must be one git accepts with plain -d.
func TestRealRepoUnforcedDeleteMatchesGit(t *testing.T) {
	repo, _ := realRepo(t)
	plan := classifyReal(t, repo, Options{
		Reasons: map[Reason]bool{ReasonMerged: true, ReasonGone: true, ReasonStale: true},
		Protect: []string{"release/*"},
	})

	unforced := 0
	for _, c := range plan.Candidates {
		if c.Force {
			continue
		}
		unforced++
		// force=false explicitly, whatever the plan said, to prove git agrees.
		if err := repo.DeleteBranch(c.Branch.Name, false); err != nil {
			t.Errorf("%s was classified as safe for -d but git refused: %v", c.Branch.Name, err)
		}
	}
	if unforced == 0 {
		t.Fatal("no unforced candidates in the plan; this test asserted nothing")
	}
}

// After any fetch, DefaultBranch() resolves to origin/main, so "merged" means
// merged into the published main. A branch merged locally but never pushed
// gets -D rather than -d — safe, but surprising enough to pin.
func TestMergedIsMeasuredAgainstTheRemoteDefaultBranch(t *testing.T) {
	requireGit(t)

	root := t.TempDir()
	local := filepath.Join(root, "local")
	remote := filepath.Join(root, "remote.git")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-q", "--bare", "-b", "main", remote)
	git(t, local, "init", "-q", "-b", "main")
	git(t, local, "config", "user.name", "Test Author")
	git(t, local, "config", "user.email", "author@example.com")
	commit(t, local, "README.md", "hello\n", "init", time.Time{})
	git(t, local, "remote", "add", "origin", remote)
	git(t, local, "push", "-q", "-u", "origin", "main")

	// Merged into local main, deliberately never pushed.
	git(t, local, "checkout", "-q", "-b", "local-merge")
	commit(t, local, "l.txt", "l\n", "local work", time.Time{})
	git(t, local, "checkout", "-q", "main")
	git(t, local, "merge", "-q", "--no-ff", "-m", "merge local-merge", "local-merge")

	repo, err := gitdata.Open(local)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := repo.Fetch(true); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if base := repo.DefaultBranch(); base != "origin/main" {
		t.Skipf("this git resolves the default branch as %q, not origin/main; "+
			"the behaviour under test does not arise", base)
	}

	branches, err := repo.Branches()
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	var got gitdata.Branch
	for _, b := range branches {
		if b.Name == "local-merge" {
			got = b
		}
	}
	if got.Name == "" {
		t.Fatal("local-merge missing from Branches()")
	}
	if got.Merged {
		t.Error("local-merge is merged only into the unpushed local main, " +
			"but was reported as Merged; tidy would offer it for -d on the " +
			"strength of a merge nobody else can see")
	}

	// git measures merged-ness against HEAD, so it accepts -d here. gitling is
	// deliberately stricter: it would force-delete a branch git would have
	// dropped unforced, never the other way round.
	if err := repo.DeleteBranch("local-merge", false); err != nil {
		t.Errorf("git refused -d on a branch merged into the checked-out main: %v", err)
	}
}

// The additive --stale contract, end to end.
func TestRealRepoStaleIsAdditive(t *testing.T) {
	repo, _ := realRepo(t)

	safe := classifyReal(t, repo, Options{
		Reasons: map[Reason]bool{ReasonMerged: true, ReasonGone: true},
		Protect: []string{"release/*"},
	})
	withStale := classifyReal(t, repo, Options{
		Reasons: map[Reason]bool{ReasonMerged: true, ReasonGone: true, ReasonStale: true},
		Protect: []string{"release/*"},
	})

	if len(withStale.Candidates) <= len(safe.Candidates) {
		t.Fatalf("adding stale gave %d candidates, was %d; stale must widen the selection",
			len(withStale.Candidates), len(safe.Candidates))
	}
	// Everything the safe selection found must survive the wider one.
	for _, c := range safe.Candidates {
		if _, ok := candidateByName(withStale, c.Branch.Name); !ok {
			t.Errorf("%s dropped out of the plan when --stale was added", c.Branch.Name)
		}
	}
	if _, ok := candidateByName(safe, "old-spike"); ok {
		t.Error("old-spike appeared without --stale being selected")
	}
}
