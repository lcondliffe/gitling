//go:build gogit

package gitdata

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runGit shells out to git to build a fixture repo. This is only used in
// test setup, never in the backend implementation itself.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test Author",
		"GIT_AUTHOR_EMAIL=author@example.com",
		"GIT_COMMITTER_NAME=Test Author",
		"GIT_COMMITTER_EMAIL=author@example.com",
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+dir, // avoid picking up the real user's global git config
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newFixtureRepo creates a temp repo with a small linear history and a
// feature branch, and returns its path.
func newFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.name", "Test Author")
	runGit(t, dir, "config", "user.email", "author@example.com")

	writeFile(t, dir, "a.txt", "one\n")
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-q", "-m", "first")

	writeFile(t, dir, "a.txt", "one\ntwo\n")
	writeFile(t, dir, "b.txt", "hello\n")
	runGit(t, dir, "add", "a.txt", "b.txt")
	runGit(t, dir, "commit", "-q", "-m", "second")

	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	writeFile(t, dir, "c.txt", "feature work\n")
	runGit(t, dir, "add", "c.txt")
	runGit(t, dir, "commit", "-q", "-m", "feature work")
	runGit(t, dir, "checkout", "-q", "main")

	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestGogitOpen(t *testing.T) {
	dir := newFixtureRepo(t)
	if _, err := openGogit(dir); err != nil {
		t.Fatalf("openGogit: %v", err)
	}
	if _, err := openGogit(t.TempDir()); err == nil {
		t.Error("openGogit on a non-repo dir: want error, got nil")
	}
}

func TestGogitHeadAndGitDir(t *testing.T) {
	dir := newFixtureRepo(t)
	g, err := openGogit(dir)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := g.Head(); err != nil {
		t.Errorf("Head: %v", err)
	}

	gitDir, err := g.GitDir()
	if err != nil {
		t.Fatalf("GitDir: %v", err)
	}
	// Resolve symlinks on both sides: on macOS, t.TempDir() lives under
	// /var, which is a symlink to /private/var, and go-git returns the
	// resolved path.
	wantResolved, err := filepath.EvalSymlinks(filepath.Join(dir, ".git"))
	if err != nil {
		t.Fatalf("EvalSymlinks(want): %v", err)
	}
	gotResolved, err := filepath.EvalSymlinks(gitDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(got): %v", err)
	}
	if gotResolved != wantResolved {
		t.Errorf("GitDir = %q, want %q", gitDir, wantResolved)
	}
}

func TestGogitCommits(t *testing.T) {
	dir := newFixtureRepo(t)
	g, err := openGogit(dir)
	if err != nil {
		t.Fatal(err)
	}

	commits, err := g.Commits("")
	if err != nil {
		t.Fatalf("Commits: %v", err)
	}
	// main has 2 non-merge commits; feature's 3rd commit isn't reachable from
	// HEAD (main) since Commits("") walks from HEAD.
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2: %+v", len(commits), commits)
	}

	// Most recent first (LogOrderCommitterTime).
	second := commits[0]
	if second.AuthorName != "Test Author" || second.AuthorEmail != "author@example.com" {
		t.Errorf("second commit author = %+v", second)
	}
	if second.Insertions != 2 || second.Deletions != 0 {
		t.Errorf("second commit stats = +%d -%d, want +2 -0", second.Insertions, second.Deletions)
	}
	wantFiles := map[string]bool{"a.txt": true, "b.txt": true}
	if len(second.Files) != 2 || !wantFiles[second.Files[0]] || !wantFiles[second.Files[1]] {
		t.Errorf("second commit files = %v", second.Files)
	}

	first := commits[1]
	if first.Insertions != 1 || first.Deletions != 0 || len(first.Files) != 1 || first.Files[0] != "a.txt" {
		t.Errorf("first commit = %+v", first)
	}

	// Incremental range: first..HEAD should only return the second commit.
	inc, err := g.Commits(first.Hash + "..HEAD")
	if err != nil {
		t.Fatalf("Commits(range): %v", err)
	}
	if len(inc) != 1 || inc[0].Hash != second.Hash {
		t.Fatalf("incremental commits = %+v, want just %q", inc, second.Hash)
	}
}

func TestGogitBranches(t *testing.T) {
	dir := newFixtureRepo(t)
	g, err := openGogit(dir)
	if err != nil {
		t.Fatal(err)
	}

	branches, err := g.Branches()
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	if len(branches) != 2 {
		t.Fatalf("got %d branches, want 2: %+v", len(branches), branches)
	}

	byName := map[string]Branch{}
	for _, b := range branches {
		byName[b.Name] = b
	}
	main, ok := byName["main"]
	if !ok || !main.IsHead {
		t.Errorf("main branch = %+v, ok=%v", main, ok)
	}
	feature, ok := byName["feature"]
	if !ok {
		t.Fatal("feature branch missing")
	}
	if feature.IsHead {
		t.Error("feature should not be HEAD")
	}
	// feature has no upstream configured, so it falls back to comparing
	// against the default branch (main): 1 commit ahead, 0 behind.
	if !feature.HasCompare || feature.Ahead != 1 || feature.Behind != 0 {
		t.Errorf("feature ahead/behind = %+v, want HasCompare=true Ahead=1 Behind=0", feature)
	}
}

func TestGogitVitals(t *testing.T) {
	dir := newFixtureRepo(t)
	g, err := openGogit(dir)
	if err != nil {
		t.Fatal(err)
	}

	v, err := g.Vitals()
	if err != nil {
		t.Fatalf("Vitals: %v", err)
	}
	if v.Branch != "main" || v.Detached {
		t.Errorf("Vitals.Branch = %q, Detached = %v", v.Branch, v.Detached)
	}
	if v.BranchCount != 2 {
		t.Errorf("BranchCount = %d, want 2", v.BranchCount)
	}
	if v.DirtyFiles != 0 {
		t.Errorf("DirtyFiles = %d, want 0 on a clean checkout", v.DirtyFiles)
	}
	if v.StashCount != 0 {
		t.Errorf("StashCount = %d, want 0 (documented gap)", v.StashCount)
	}

	writeFile(t, dir, "untracked.txt", "x\n")
	v2, err := g.Vitals()
	if err != nil {
		t.Fatalf("Vitals (dirty): %v", err)
	}
	if v2.DirtyFiles != 1 {
		t.Errorf("DirtyFiles after adding untracked file = %d, want 1", v2.DirtyFiles)
	}
}

func TestGogitIsAncestor(t *testing.T) {
	dir := newFixtureRepo(t)
	g, err := openGogit(dir)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := g.Commits("")
	if err != nil {
		t.Fatal(err)
	}
	first, second := commits[1].Hash, commits[0].Hash

	if !g.IsAncestor(first, second) {
		t.Error("IsAncestor(first, second) = false, want true")
	}
	if g.IsAncestor(second, first) {
		t.Error("IsAncestor(second, first) = true, want false")
	}
	if g.IsAncestor("", second) {
		t.Error("IsAncestor(\"\", second) = true, want false")
	}
}
