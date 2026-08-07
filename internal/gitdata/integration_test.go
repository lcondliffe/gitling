package gitdata

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Integration tests driving a real git against a real repository in
// t.TempDir(). They skip when git is missing rather than failing, so a bare
// `go test ./...` still runs them wherever git exists.

// requireGit skips the calling test when there is no usable git on PATH.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH; skipping integration test")
	}
}

// gitCmd runs git in dir and fails the test if it errors. Setup only.
func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gitCmdAt(t, dir, time.Time{}, args...)
}

// gitCmdAt is gitCmd with a fixed commit timestamp, for backdating.
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

// fixture is a local repository with a bare remote, so upstream tracking (and
// therefore Branch.Gone) means something.
type fixture struct {
	local  string
	remote string
}

// newFixture builds a repo with a real origin and one commit on main.
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
func (f fixture) repo(t *testing.T) *Repo {
	t.Helper()
	r, err := Open(f.local)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return r
}

// branchOnRemoteDeleted deletes a branch inside the bare remote directly.
// `git push origin --delete` would also drop the local tracking ref, leaving
// nothing for a pruning fetch to find.
func (f fixture) branchOnRemoteDeleted(t *testing.T, name string) {
	t.Helper()
	gitCmd(t, f.remote, "branch", "-D", name)
}

// branches indexes Branches() by name for assertions.
func branchesByName(t *testing.T, r *Repo) map[string]Branch {
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

// Merged decides -d versus -D; Tip is the printed restore hash.
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

// The squash-merge shape: work is in main under a new hash, so git sees the
// branch as unmerged and only the deleted remote branch vouches for it.
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

// Fetch(true) is what makes Gone mean anything; Fetch(false) must not prune.
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

// A merged branch goes with -d; a squash-merged one is refused and needs -D.
// The refusal is git's own merge check, kept as a safety net under tidy's.
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

// tidy protects the current branch, but git is the backstop if that regresses.
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

// The restore instruction tidy prints must actually work, using the hash the
// tool itself reported.
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

// LastCommit must reflect the real committer date; the stale threshold
// measures against it.
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

// The ref everything else is measured against.
func TestIntegrationDefaultBranch(t *testing.T) {
	f := newFixture(t)
	if got := f.repo(t).DefaultBranch(); got != "main" {
		t.Errorf("DefaultBranch() = %q, want %q", got, "main")
	}
}

// A non-repository must fail rather than behave as one.
func TestIntegrationOpenRejectsNonRepo(t *testing.T) {
	requireGit(t)
	if _, err := Open(t.TempDir()); err == nil {
		t.Error("Open on a non-repository should fail")
	}
}

// git refuses to delete a branch checked out in another worktree, so Branches
// must report where it is checked out.
func TestIntegrationBranchInOtherWorktree(t *testing.T) {
	f := newFixture(t)

	gitCmd(t, f.local, "checkout", "-q", "-b", "in-worktree")
	commitFile(t, f.local, "w.txt", "w\n", "worktree work", time.Time{})
	gitCmd(t, f.local, "checkout", "-q", "main")
	gitCmd(t, f.local, "merge", "-q", "--no-ff", "-m", "merge", "in-worktree")

	wt := filepath.Join(t.TempDir(), "linked")
	gitCmd(t, f.local, "worktree", "add", "-q", wt, "in-worktree")

	r := f.repo(t)
	got := branchesByName(t, r)["in-worktree"]
	if got.Worktree == "" {
		t.Fatal("Worktree not reported for a branch checked out in a linked worktree")
	}
	if !got.Merged {
		t.Error("precondition: the branch should still read as merged")
	}

	// Merged, so tidy would otherwise offer it for a plain -d.
	if err := r.DeleteBranch("in-worktree", false); err == nil {
		t.Error("deleting a branch checked out in another worktree should be refused")
	}
	if !localBranchExists(t, f.local, "in-worktree") {
		t.Error("branch was deleted despite being checked out elsewhere")
	}
}
