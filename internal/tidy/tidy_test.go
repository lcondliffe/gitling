package tidy

import (
	"testing"
	"time"

	"github.com/lcondliffe/gitling/internal/gitdata"
)

var now = time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

func branch(name string, opts ...func(*gitdata.Branch)) gitdata.Branch {
	b := gitdata.Branch{Name: name, LastCommit: now.AddDate(0, 0, -1), Tip: "abc1234"}
	for _, o := range opts {
		o(&b)
	}
	return b
}

func merged(b *gitdata.Branch)   { b.Merged = true }
func gone(b *gitdata.Branch)     { b.Gone = true }
func head(b *gitdata.Branch)     { b.IsHead = true }
func old(b *gitdata.Branch)      { b.LastCommit = now.AddDate(0, 0, -200) }
func noCommit(b *gitdata.Branch) { b.LastCommit = time.Time{} }

func names(cs []Candidate) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Branch.Name)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestClassifyDefaultsToMergedAndGone(t *testing.T) {
	branches := []gitdata.Branch{
		branch("main", head),
		branch("chore/tidy", merged),
		branch("feat/x", gone),
		branch("old/thing", old),
		branch("live/work"),
	}
	plan := Classify(branches, Options{Base: "origin/main", Now: now})

	if want := []string{"chore/tidy", "feat/x"}; !equal(names(plan.Candidates), want) {
		t.Errorf("candidates = %v, want %v", names(plan.Candidates), want)
	}
	if plan.Total != 5 {
		t.Errorf("Total = %d, want 5", plan.Total)
	}
}

// The reason decides -d vs -D, so it is the safety-critical part of the plan:
// only a provably merged branch may be deleted without forcing.
func TestClassifyForcesEverythingButMerged(t *testing.T) {
	plan := Classify([]gitdata.Branch{
		branch("chore/tidy", merged),
		branch("feat/x", gone),
		branch("old/thing", old),
	}, Options{
		Base:    "main",
		Reasons: map[Reason]bool{ReasonMerged: true, ReasonGone: true, ReasonStale: true},
		Now:     now,
	})

	want := map[string]bool{"chore/tidy": false, "feat/x": true, "old/thing": true}
	for _, c := range plan.Candidates {
		if c.Force != want[c.Branch.Name] {
			t.Errorf("%s Force = %v, want %v", c.Branch.Name, c.Force, want[c.Branch.Name])
		}
	}
	if plan.Forced() != 2 {
		t.Errorf("Forced() = %d, want 2", plan.Forced())
	}
}

// A branch that is both merged and old must be reported as merged, so it is
// deleted with -d rather than forced.
func TestClassifyPrefersTheSafestApplicableReason(t *testing.T) {
	plan := Classify([]gitdata.Branch{
		branch("both", merged, old),
		branch("goneAndOld", gone, old),
	}, Options{
		Base:    "main",
		Reasons: map[Reason]bool{ReasonMerged: true, ReasonGone: true, ReasonStale: true},
		Now:     now,
	})

	byName := map[string]Candidate{}
	for _, c := range plan.Candidates {
		byName[c.Branch.Name] = c
	}
	if got := byName["both"]; got.Reason != ReasonMerged || got.Force {
		t.Errorf("merged+old = %+v, want merged and unforced", got)
	}
	if got := byName["goneAndOld"]; got.Reason != ReasonGone {
		t.Errorf("gone+old reason = %q, want %q", got.Reason, ReasonGone)
	}
}

func TestClassifyStaleOnlyWhenSelected(t *testing.T) {
	branches := []gitdata.Branch{branch("old/thing", old)}

	if plan := Classify(branches, Options{Base: "main", Now: now}); !plan.Empty() {
		t.Errorf("stale branch selected by default: %v", names(plan.Candidates))
	}

	plan := Classify(branches, Options{
		Base:    "main",
		Reasons: map[Reason]bool{ReasonMerged: true, ReasonGone: true, ReasonStale: true},
		Now:     now,
	})
	if want := []string{"old/thing"}; !equal(names(plan.Candidates), want) {
		t.Errorf("candidates = %v, want %v", names(plan.Candidates), want)
	}
}

func TestClassifyStaleThreshold(t *testing.T) {
	branches := []gitdata.Branch{
		branch("d100", func(b *gitdata.Branch) { b.LastCommit = now.AddDate(0, 0, -100) }),
		branch("d200", func(b *gitdata.Branch) { b.LastCommit = now.AddDate(0, 0, -200) }),
	}
	opts := Options{Base: "main", Reasons: map[Reason]bool{ReasonStale: true}, Now: now}

	// The 90-day default catches both.
	if got := names(Classify(branches, opts).Candidates); len(got) != 2 {
		t.Errorf("default threshold selected %v, want both", got)
	}

	opts.StaleAfter = 180 * 24 * time.Hour
	if want := []string{"d200"}; !equal(names(Classify(branches, opts).Candidates), want) {
		t.Errorf("180d threshold selected %v, want %v", names(Classify(branches, opts).Candidates), want)
	}
}

// A branch with no readable commit date can't be aged, so it must never be
// swept up as stale.
func TestClassifyNeverStalesAnUndatedBranch(t *testing.T) {
	plan := Classify([]gitdata.Branch{branch("weird", noCommit)},
		Options{Base: "main", Reasons: map[Reason]bool{ReasonStale: true}, Now: now})
	if !plan.Empty() {
		t.Errorf("undated branch selected: %v", names(plan.Candidates))
	}
}

func TestClassifyProtectsCurrentAndDefaultBranches(t *testing.T) {
	// Standing on a merged feature branch: it must survive, and be reported.
	plan := Classify([]gitdata.Branch{
		branch("feature", merged, head),
		branch("main", merged),
	}, Options{Base: "origin/main", Now: now})

	if !plan.Empty() {
		t.Fatalf("candidates = %v, want none", names(plan.Candidates))
	}
	// The default branch surviving goes without saying; the checked-out one
	// does not, since it would otherwise have been deleted.
	if len(plan.Protected) != 1 || plan.Protected[0].Branch.Name != "feature" {
		t.Fatalf("Protected = %+v, want just the checked-out branch", plan.Protected)
	}
	if plan.Protected[0].Why != "checked out" {
		t.Errorf("Why = %q, want %q", plan.Protected[0].Why, "checked out")
	}
}

// Base is a remote ref, so the local branch of the same name has to be matched
// against it too or the default branch gets deleted.
func TestClassifyProtectsLocalDefaultBranchAgainstRemoteBase(t *testing.T) {
	plan := Classify([]gitdata.Branch{branch("main", merged)},
		Options{Base: "origin/main", Now: now})
	if !plan.Empty() {
		t.Errorf("deleted the default branch: %v", names(plan.Candidates))
	}
	if len(plan.Protected) != 0 {
		t.Errorf("Protected = %+v, want the default branch kept quietly", plan.Protected)
	}
}

func TestClassifyProtectGlobs(t *testing.T) {
	plan := Classify([]gitdata.Branch{
		branch("release/1.x", merged),
		branch("release/2.x", gone),
		branch("chore/tidy", merged),
	}, Options{Base: "main", Protect: []string{"release/*"}, Now: now})

	if want := []string{"chore/tidy"}; !equal(names(plan.Candidates), want) {
		t.Errorf("candidates = %v, want %v", names(plan.Candidates), want)
	}
	if len(plan.Protected) != 2 {
		t.Fatalf("Protected = %+v, want both release branches", plan.Protected)
	}
	if plan.Protected[0].Why != "protected by release/*" {
		t.Errorf("Why = %q", plan.Protected[0].Why)
	}
}

// A glob that cannot be evaluated must keep the branch, not expose it. The
// command layer rejects malformed patterns up front; this is the backstop.
func TestClassifyKeepsBranchesUnderMalformedGlob(t *testing.T) {
	plan := Classify([]gitdata.Branch{branch("chore/tidy", merged)},
		Options{Base: "main", Protect: []string{"[bad"}, Now: now})
	if len(plan.Candidates) != 0 {
		t.Errorf("candidates = %v, want none: a branch must not become deletable because its protect pattern is unusable", names(plan.Candidates))
	}
	if len(plan.Protected) != 1 || plan.Protected[0].Why != "unusable protect pattern [bad" {
		t.Errorf("Protected = %+v, want the branch kept with a reason naming the pattern", plan.Protected)
	}
}

// Grouped safest-first, oldest-first within a group: the order you'd work
// through them in.
func TestClassifyOrdersByReasonThenAge(t *testing.T) {
	plan := Classify([]gitdata.Branch{
		branch("stale-newer", func(b *gitdata.Branch) { b.LastCommit = now.AddDate(0, 0, -100) }),
		branch("gone-newer", gone),
		branch("merged-b", merged),
		branch("stale-older", old),
		branch("gone-older", gone, func(b *gitdata.Branch) { b.LastCommit = now.AddDate(0, 0, -300) }),
		branch("merged-a", merged, func(b *gitdata.Branch) { b.LastCommit = now.AddDate(0, 0, -5) }),
	}, Options{
		Base:    "main",
		Reasons: map[Reason]bool{ReasonMerged: true, ReasonGone: true, ReasonStale: true},
		Now:     now,
	})

	want := []string{"merged-a", "merged-b", "gone-older", "gone-newer", "stale-older", "stale-newer"}
	if !equal(names(plan.Candidates), want) {
		t.Errorf("order = %v, want %v", names(plan.Candidates), want)
	}
}

func TestClassifyNarrowsToSelectedReason(t *testing.T) {
	branches := []gitdata.Branch{branch("chore/tidy", merged), branch("feat/x", gone)}

	plan := Classify(branches, Options{Base: "main", Reasons: map[Reason]bool{ReasonMerged: true}, Now: now})
	if want := []string{"chore/tidy"}; !equal(names(plan.Candidates), want) {
		t.Errorf("--merged selected %v, want %v", names(plan.Candidates), want)
	}

	plan = Classify(branches, Options{Base: "main", Reasons: map[Reason]bool{ReasonGone: true}, Now: now})
	if want := []string{"feat/x"}; !equal(names(plan.Candidates), want) {
		t.Errorf("--gone selected %v, want %v", names(plan.Candidates), want)
	}
}

func TestPlanEmpty(t *testing.T) {
	if !(Plan{}).Empty() {
		t.Error("zero Plan should be empty")
	}
	if (Plan{Candidates: []Candidate{{}}}).Empty() {
		t.Error("plan with a candidate should not be empty")
	}
}
