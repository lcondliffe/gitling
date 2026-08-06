//go:build gogit

package gitdata

import (
	"errors"
	"testing"
)

// TestGogitRefusesWrites pins the gogit backend's read-only contract. tidy is
// the only caller that writes, and under this build it must fail loudly rather
// than appear to work: a half-implemented DeleteBranch that silently did
// nothing would leave the user believing branches were cleaned up when they
// were not, and a partially-implemented one is worse still.
func TestGogitRefusesWrites(t *testing.T) {
	dir := newFixtureRepo(t)
	g, err := openGogit(dir)
	if err != nil {
		t.Fatalf("openGogit: %v", err)
	}

	if err := g.Fetch(true); !errors.Is(err, errReadOnly) {
		t.Errorf("Fetch = %v, want errReadOnly", err)
	}
	if err := g.DeleteBranch("feature", false); !errors.Is(err, errReadOnly) {
		t.Errorf("DeleteBranch = %v, want errReadOnly", err)
	}
	if err := g.DeleteBranch("feature", true); !errors.Is(err, errReadOnly) {
		t.Errorf("DeleteBranch(force) = %v, want errReadOnly", err)
	}

	// Refusing has to mean not deleting.
	if !localBranchExists(t, dir, "feature") {
		t.Error("feature branch was deleted by a backend that reports itself read-only")
	}
}
