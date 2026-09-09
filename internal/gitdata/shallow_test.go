package gitdata

import (
	"path/filepath"
	"testing"
)

func TestIsShallow(t *testing.T) {
	f := newFixture(t)
	if f.repo(t).IsShallow() {
		t.Fatal("ordinary repo reported shallow")
	}
	root := t.TempDir()
	clone := filepath.Join(root, "shallow")
	gitCmd(t, root, "clone", "--no-local", "--depth=1", f.remote, clone)
	repo, err := Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	if !repo.IsShallow() {
		t.Fatal("depth-one clone not reported shallow")
	}
}
