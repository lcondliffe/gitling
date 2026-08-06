package gitdata

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Integration tests for the shell-out git layer: everything below here drives a
// real `git` against a real repository built in t.TempDir(). The parser tests
// elsewhere in this package prove we read git's output correctly; these prove
// we asked git the right question in the first place, which is the half that
// matters for a command that deletes branches.
//
// Gating: these skip when git is missing rather than failing (see requireGit).
// A build tag was the alternative and was rejected — CI runs a bare
// `go test ./...`, so a tag would mean the write layer's only tests never run
// on any pull request, which is the situation this suite exists to end.
//

// requireGit skips the calling test when there is no usable git on PATH.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH; skipping integration test")
	}
}

// gitCmd runs git in dir and fails the test if it errors. Setup only — never
// used to assert behaviour, so a test can't pass by re-implementing the thing
// it is meant to be checking.
func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gitCmdAt(t, dir, time.Time{}, args...)
}

// gitCmdAt is gitCmd with a fixed commit timestamp, so a fixture can contain a
// branch that has genuinely not been touched for a year.
func gitCmdAt(t *testing.T, dir string, when time.Time, args ...string) string {
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

func writeRepoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commitFile writes a file and commits it, optionally backdated.
func commitFile(t *testing.T, dir, name, content, msg string, when time.Time) {
	t.Helper()
	writeRepoFile(t, dir, name, content)
	gitCmd(t, dir, "add", name)
	gitCmdAt(t, dir, when, "commit", "-q", "-m", msg)
}

// fixture is a local repository with a bare "remote" it can push to, which is
// what makes upstream tracking — and therefore Branch.Gone — mean anything.
type fixture struct {
	local  string
	remote string
}

// newFixture builds a repo with a real origin and a main branch with one
// commit. Individual tests add the branch shapes they need, so each test's
// setup reads as the scenario it is testing.
func newFixture(t *testing.T) fixture {
	t.Helper()
	requireGit(t)

	root := t.TempDir()
	f := fixture{
		local:  filepath.Join(root, "local"),
		remote: filepath.Join(root, "remote.git"),
	}
	if err := os.MkdirAll(f.local, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, root, "init", "-q", "--bare", "-b", "main", f.remote)
	gitCmd(t, f.local, "init", "-q", "-b", "main")
	gitCmd(t, f.local, "config", "user.name", "Test Author")
	gitCmd(t, f.local, "config", "user.email", "author@example.com")

	commitFile(t, f.local, "README.md", "hello\n", "init", time.Time{})
	gitCmd(t, f.local, "remote", "add", "origin", f.remote)
	gitCmd(t, f.local, "push", "-q", "-u", "origin", "main")
	return f
}

// repo opens the fixture with the shell backend under test.
func (f fixture) repo(t *testing.T) *shellRepo {
	t.Helper()
	r, err := openShell(f.local)
	if err != nil {
		t.Fatalf("openShell: %v", err)
	}
	return r
}

// branchOnRemoteDeleted removes a branch inside the bare remote directly,
// rather than pushing a delete from the clone. `git push origin --delete`
// would also drop the local remote-tracking ref as a side effect, leaving
// nothing for a pruning fetch to find — this reproduces the real shape, where
// someone else's merge deleted the branch and this clone has not heard yet.
func (f fixture) branchOnRemoteDeleted(t *testing.T, name string) {
	t.Helper()
	gitCmd(t, f.remote, "branch", "-D", name)
}

// branches indexes Branches() by name for assertions.
func branchesByName(t *testing.T, r *shellRepo) map[string]Branch {
	t.Helper()
	got, err := r.Branches()
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	m := map[string]Branch{}
	for _, b := range got {
		m[b.Name] = b
	}
	return m
}

func localBranchExists(t *testing.T, dir, name string) bool {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	cmd.Dir = dir
	return cmd.Run() == nil
}

func refExists(t *testing.T, dir, ref string) bool {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", ref)
	cmd.Dir = dir
	return cmd.Run() == nil
}

// TestIntegrationBranchesMergedAndTip covers the two fields the tidy command's
// safety rests on. Merged decides -d versus -D; Tip is the hash printed as the
// restore instruction. Both are populated by Branches and neither had a test.
func TestIntegrationBranchesMergedAndTip(t *testing.T) {
	f := newFixture(t)

	// A genuinely merged branch: its commit becomes an ancestor of main.
	gitCmd(t, f.local, "checkout", "-q", "-b", "merged-work")
	commitFile(t, f.local, "merged.txt", "work\n", "merged work", time.Time{})
	gitCmd(t, f.local, "checkout", "-q", "main")
	gitCmd(t, f.local, "merge", "-q", "--no-ff", "-m", "merge merged-work", "merged-work")

	// An unmerged branch: real commits that never landed.
	gitCmd(t, f.local, "checkout", "-q", "-b", "unmerged-work")
	commitFile(t, f.local, "unmerged.txt", "wip\n", "wip", time.Time{})
	gitCmd(t, f.local, "checkout", "-q", "main")

	r := f.repo(t)
	got := branchesByName(t, r)

	if !got["merged-work"].Merged {
		t.Error("merged-work was merged into main but Merged is false; tidy would force-delete a branch git would have accepted with -d")
	}
	if got["unmerged-work"].Merged {
		t.Error("unmerged-work has unlanded commits but Merged is true; tidy would offer it as a safe -d deletion")
	}
	if !got["main"].Merged {
		t.Error("main should count as merged into itself")
	}

	// Tip must be the real hash, not an empty string or an abbreviation.
	for _, name := range []string{"main", "merged-work", "unmerged-work"} {
		want := gitCmd(t, f.local, "rev-parse", name)
		if got[name].Tip != want {
			t.Errorf("%s Tip = %q, want %q", name, got[name].Tip, want)
		}
		if len(got[name].Tip) != 40 {
			t.Errorf("%s Tip = %q, want a full 40-char hash", name, got[name].Tip)
		}
	}
}

// TestIntegrationSquashMergedIsGoneNotMerged covers the squash-merge shape,
// which is the whole reason tidy has a --gone category. The work is in main
// under a new hash, so git does not consider the branch merged; the evidence
// that it is safe is that the remote branch was deleted.
func TestIntegrationSquashMergedIsGoneNotMerged(t *testing.T) {
	f := newFixture(t)

	gitCmd(t, f.local, "checkout", "-q", "-b", "squashed-work")
	commitFile(t, f.local, "feature.txt", "feature\n", "feature work", time.Time{})
	gitCmd(t, f.local, "push", "-q", "-u", "origin", "squashed-work")

	// Squash-merge into main: same content, brand new commit.
	gitCmd(t, f.local, "checkout", "-q", "main")
	gitCmd(t, f.local, "merge", "-q", "--squash", "squashed-work")
	gitCmd(t, f.local, "commit", "-q", "-m", "feature work (squashed) (#1)")
	gitCmd(t, f.local, "push", "-q", "origin", "main")

	// The forge deletes the branch on merge; this clone doesn't know yet.
	f.branchOnRemoteDeleted(t, "squashed-work")

	r := f.repo(t)
	if err := r.Fetch(true); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	got := branchesByName(t, r)["squashed-work"]
	if got.Merged {
		t.Error("squash-merged branch reported as Merged; git cannot verify this and -d would be wrong")
	}
	if !got.Gone {
		t.Error("squash-merged branch with a deleted upstream should be Gone")
	}
}

// TestIntegrationFetchPrune proves Fetch(true) is what makes Gone mean
// anything, and that Fetch(false) leaves the stale ref alone.
func TestIntegrationFetchPrune(t *testing.T) {
	t.Run("prune removes the stale remote-tracking ref", func(t *testing.T) {
		f := newFixture(t)
		gitCmd(t, f.local, "checkout", "-q", "-b", "doomed")
		commitFile(t, f.local, "d.txt", "d\n", "doomed work", time.Time{})
		gitCmd(t, f.local, "push", "-q", "-u", "origin", "doomed")
		gitCmd(t, f.local, "checkout", "-q", "main")

		f.branchOnRemoteDeleted(t, "doomed")

		r := f.repo(t)
		if !refExists(t, f.local, "refs/remotes/origin/doomed") {
			t.Fatal("precondition: remote-tracking ref should still exist before the pruning fetch")
		}
		if branchesByName(t, r)["doomed"].Gone {
			t.Error("branch reported Gone before any fetch; the fixture is not proving anything")
		}

		if err := r.Fetch(true); err != nil {
			t.Fatalf("Fetch(true): %v", err)
		}
		if refExists(t, f.local, "refs/remotes/origin/doomed") {
			t.Error("Fetch(true) did not prune the deleted upstream's tracking ref")
		}
		if !branchesByName(t, r)["doomed"].Gone {
			t.Error("branch should be Gone after a pruning fetch")
		}
	})

	t.Run("without prune the stale ref survives", func(t *testing.T) {
		f := newFixture(t)
		gitCmd(t, f.local, "checkout", "-q", "-b", "kept")
		commitFile(t, f.local, "k.txt", "k\n", "kept work", time.Time{})
		gitCmd(t, f.local, "push", "-q", "-u", "origin", "kept")
		gitCmd(t, f.local, "checkout", "-q", "main")

		f.branchOnRemoteDeleted(t, "kept")

		r := f.repo(t)
		if err := r.Fetch(false); err != nil {
			t.Fatalf("Fetch(false): %v", err)
		}
		if !refExists(t, f.local, "refs/remotes/origin/kept") {
			t.Error("Fetch(false) pruned the tracking ref; prune must be opt-in")
		}
	})
}

// TestIntegrationDeleteBranch is the acceptance criterion in one test: a merged
// branch goes with -d, a squash-merged one is refused by -d and needs -D. The
// refusal is git's own merge check, which tidy keeps underneath its own
// classification as a safety net — so it has to actually be there.
func TestIntegrationDeleteBranch(t *testing.T) {
	f := newFixture(t)

	// Merged: deletable without force.
	gitCmd(t, f.local, "checkout", "-q", "-b", "merged-work")
	commitFile(t, f.local, "m.txt", "m\n", "merged work", time.Time{})
	gitCmd(t, f.local, "checkout", "-q", "main")
	gitCmd(t, f.local, "merge", "-q", "--no-ff", "-m", "merge", "merged-work")

	// Squash-merged: content is in main, commit is not.
	gitCmd(t, f.local, "checkout", "-q", "-b", "squashed-work")
	commitFile(t, f.local, "s.txt", "s\n", "squashed work", time.Time{})
	gitCmd(t, f.local, "checkout", "-q", "main")
	gitCmd(t, f.local, "merge", "-q", "--squash", "squashed-work")
	gitCmd(t, f.local, "commit", "-q", "-m", "squashed (#2)")

	r := f.repo(t)

	if err := r.DeleteBranch("merged-work", false); err != nil {
		t.Errorf("DeleteBranch(merged-work, force=false) = %v, want success: git verified this branch is merged", err)
	}
	if localBranchExists(t, f.local, "merged-work") {
		t.Error("merged-work still exists after an unforced delete")
	}

	if err := r.DeleteBranch("squashed-work", false); err == nil {
		t.Error("DeleteBranch(squashed-work, force=false) succeeded; git's merge check should refuse a squash-merged branch")
	}
	if !localBranchExists(t, f.local, "squashed-work") {
		t.Fatal("squashed-work was deleted by the call that should have been refused")
	}

	if err := r.DeleteBranch("squashed-work", true); err != nil {
		t.Errorf("DeleteBranch(squashed-work, force=true) = %v, want success", err)
	}
	if localBranchExists(t, f.local, "squashed-work") {
		t.Error("squashed-work still exists after a forced delete")
	}
}

// TestIntegrationDeleteBranchRefusesCheckedOut covers the other deletion git
// refuses outright. tidy protects the current branch itself, but if that ever
// regressed, git is the backstop — including under -D.
func TestIntegrationDeleteBranchRefusesCheckedOut(t *testing.T) {
	f := newFixture(t)
	r := f.repo(t)

	if err := r.DeleteBranch("main", false); err == nil {
		t.Error("deleting the checked-out branch should be refused")
	}
	if err := r.DeleteBranch("main", true); err == nil {
		t.Error("deleting the checked-out branch should be refused even with force")
	}
	if !localBranchExists(t, f.local, "main") {
		t.Fatal("main was deleted")
	}
}

// TestIntegrationDeletedBranchIsRestorable is the safety argument for allowing
// -D at all: tidy prints "restore any of these with git branch <name> <hash>",
// and this proves that instruction actually works, using the hash the tool
// itself reported rather than one the test looked up separately.
func TestIntegrationDeletedBranchIsRestorable(t *testing.T) {
	f := newFixture(t)

	gitCmd(t, f.local, "checkout", "-q", "-b", "lost-work")
	commitFile(t, f.local, "precious.txt", "irreplaceable\n", "precious work", time.Time{})
	gitCmd(t, f.local, "checkout", "-q", "main")

	r := f.repo(t)

	// The hash exactly as the user would read it off tidy's output.
	tip := branchesByName(t, r)["lost-work"].Tip
	if tip == "" {
		t.Fatal("no Tip reported; the restore instruction would be unusable")
	}

	if err := r.DeleteBranch("lost-work", true); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	if localBranchExists(t, f.local, "lost-work") {
		t.Fatal("branch was not deleted")
	}

	// Exactly what the tool told the user to type.
	gitCmd(t, f.local, "branch", "lost-work", tip)

	if !localBranchExists(t, f.local, "lost-work") {
		t.Fatal("restore did not recreate the branch")
	}
	if got := gitCmd(t, f.local, "rev-parse", "lost-work"); got != tip {
		t.Errorf("restored branch points at %s, want %s", got, tip)
	}
	// The commit is back, and so is its content.
	if got := gitCmd(t, f.local, "show", tip+":precious.txt"); got != "irreplaceable" {
		t.Errorf("restored file content = %q, want %q", got, "irreplaceable")
	}
}

// TestIntegrationStaleBranchDates proves LastCommit reflects the real committer
// date, which is what the stale threshold is measured against. A backdated
// fixture is the only way to test this without waiting 90 days.
func TestIntegrationStaleBranchDates(t *testing.T) {
	f := newFixture(t)

	old := time.Now().AddDate(0, 0, -400).Truncate(time.Second)
	gitCmd(t, f.local, "checkout", "-q", "-b", "old-spike")
	commitFile(t, f.local, "spike.txt", "spike\n", "old spike", old)
	gitCmd(t, f.local, "checkout", "-q", "main")

	got := branchesByName(t, f.repo(t))["old-spike"]
	if got.LastCommit.IsZero() {
		t.Fatal("LastCommit not populated; a branch with no date is never stale")
	}
	if !got.LastCommit.Equal(old) {
		t.Errorf("LastCommit = %v, want %v", got.LastCommit.UTC(), old.UTC())
	}
	if age := time.Since(got.LastCommit); age < 399*24*time.Hour {
		t.Errorf("backdating did not take: branch reads as %v old", age)
	}
}

// TestIntegrationDefaultBranch covers the ref every other answer is measured
// against. Getting it wrong makes "merged" meaningless.
func TestIntegrationDefaultBranch(t *testing.T) {
	f := newFixture(t)
	if got := f.repo(t).DefaultBranch(); got != "main" {
		t.Errorf("DefaultBranch() = %q, want %q", got, "main")
	}
}

// TestIntegrationOpenRejectsNonRepo keeps the error path honest: a directory
// that is not a repository must fail rather than silently behaving as one.
func TestIntegrationOpenRejectsNonRepo(t *testing.T) {
	requireGit(t)
	if _, err := openShell(t.TempDir()); err == nil {
		t.Error("openShell on a non-repository should fail")
	}
}
