// Package tidy decides which local branches are cleanup candidates.
//
// It is deliberately pure: it takes the branch list gitdata produced and
// returns a plan, without touching the repository. Everything that can delete
// something lives in the command layer, driven by a plan this package built and
// the user approved — so the risky half of `gitling tidy` has exactly one
// entry point, and the half with all the judgement in it is a function you can
// test by calling it.
package tidy

import (
	"path"
	"sort"
	"time"

	"github.com/lcondliffe/gitling/internal/gitdata"
)

// Reason is why a branch is a cleanup candidate. The three overlap in practice;
// Classify assigns each branch the safest single reason that applies.
type Reason string

const (
	// ReasonMerged: the tip is an ancestor of the default branch, so the work is
	// verifiably in and `git branch -d` will accept the deletion.
	ReasonMerged Reason = "merged"
	// ReasonGone: the branch tracked a remote branch that no longer exists.
	// This is the squash-merge shape — the commits landed under new hashes, so
	// git sees the branch as unmerged and only -D will drop it. The forge
	// deleting the remote branch on merge is the evidence that it is safe.
	ReasonGone Reason = "gone"
	// ReasonStale: no commit for longer than the threshold, and neither merged
	// nor gone. This is the category where deleting can actually lose work, so
	// it is never selected unless asked for explicitly.
	ReasonStale Reason = "stale"
)

// DefaultStaleAfter is the age at which an untouched branch becomes a stale
// candidate, matching the dashboard's branch-health threshold.
const DefaultStaleAfter = gitdata.StaleBranchDays * 24 * time.Hour

// Options configures Classify.
type Options struct {
	// Base is the default-branch ref that "merged" was measured against, e.g.
	// "origin/main". Used for protection and for labelling the plan.
	Base string
	// Reasons selects the categories to consider. An empty set means the safe
	// default: merged and gone.
	Reasons map[Reason]bool
	// StaleAfter overrides DefaultStaleAfter when non-zero.
	StaleAfter time.Duration
	// Protect holds additional filepath-style globs ("release/*") that are never
	// deleted, on top of the current and default branches.
	Protect []string
	Now     time.Time
}

// selects reports whether a reason is in play, defaulting to merged+gone.
func (o Options) selects(r Reason) bool {
	if len(o.Reasons) == 0 {
		return r == ReasonMerged || r == ReasonGone
	}
	return o.Reasons[r]
}

func (o Options) staleAfter() time.Duration {
	if o.StaleAfter > 0 {
		return o.StaleAfter
	}
	return DefaultStaleAfter
}

// Candidate is one branch the plan proposes to delete.
type Candidate struct {
	Branch gitdata.Branch
	Reason Reason
	// Force means git's own merge check would refuse this deletion and tidy
	// would have to pass -D. True for every reason except ReasonMerged.
	Force bool
}

// Protected is a branch that matched a selected category but is being kept, and
// why — reported so a branch never silently goes missing from the plan.
type Protected struct {
	Branch gitdata.Branch
	Why    string
}

// Plan is the full result of classifying a repository's branches.
type Plan struct {
	Candidates []Candidate
	Protected  []Protected
	Base       string
	StaleAfter time.Duration
	// Total is how many local branches were considered.
	Total int
}

// Empty reports whether the plan would delete nothing.
func (p Plan) Empty() bool { return len(p.Candidates) == 0 }

// Forced is how many candidates need -D.
func (p Plan) Forced() int {
	n := 0
	for _, c := range p.Candidates {
		if c.Force {
			n++
		}
	}
	return n
}

// Classify sorts branches into deletion candidates and protected keepers.
//
// A branch gets at most one reason, chosen by how much evidence there is that
// its work is safe: merged (provably in) beats gone (the remote was deleted,
// which is strong but circumstantial) beats stale (no evidence at all, just
// age). That ordering matters because the reason decides whether -d or -D is
// used, and picking the safest applicable reason means never forcing a deletion
// that git would have accepted on its own.
func Classify(branches []gitdata.Branch, opts Options) Plan {
	plan := Plan{
		Base:       opts.Base,
		StaleAfter: opts.staleAfter(),
		Total:      len(branches),
	}
	cutoff := opts.Now.Add(-opts.staleAfter())

	for _, b := range branches {
		reason, ok := classifyOne(b, opts, cutoff)
		if !ok {
			continue
		}
		if why, report := protectedBecause(b, opts); why != "" {
			if report {
				plan.Protected = append(plan.Protected, Protected{Branch: b, Why: why})
			}
			continue
		}
		plan.Candidates = append(plan.Candidates, Candidate{
			Branch: b,
			Reason: reason,
			Force:  reason != ReasonMerged,
		})
	}

	// Group by reason (merged, gone, stale — safest first), then oldest first
	// within a group, so the output reads as a to-do list in the order a person
	// would work through it.
	order := map[Reason]int{ReasonMerged: 0, ReasonGone: 1, ReasonStale: 2}
	sort.SliceStable(plan.Candidates, func(i, j int) bool {
		a, b := plan.Candidates[i], plan.Candidates[j]
		if order[a.Reason] != order[b.Reason] {
			return order[a.Reason] < order[b.Reason]
		}
		return a.Branch.LastCommit.Before(b.Branch.LastCommit)
	})
	return plan
}

func classifyOne(b gitdata.Branch, opts Options, cutoff time.Time) (Reason, bool) {
	if b.Merged && opts.selects(ReasonMerged) {
		return ReasonMerged, true
	}
	if b.Gone && opts.selects(ReasonGone) {
		return ReasonGone, true
	}
	// Stale only covers what the other two didn't: a branch that is merged or
	// gone is reported as such regardless of age.
	if opts.selects(ReasonStale) && !b.Merged && !b.Gone &&
		!b.LastCommit.IsZero() && b.LastCommit.Before(cutoff) {
		return ReasonStale, true
	}
	return "", false
}

// protectedBecause returns why a branch must be kept, or "" when it may go.
// report distinguishes a protection worth telling the user about from one that
// goes without saying: nobody needs to be told that the default branch survived
// a cleanup, but "you're standing on one of these" and "your config saved this
// one" are both worth a line.
func protectedBecause(b gitdata.Branch, opts Options) (why string, report bool) {
	// Checked before IsHead so that standing on the default branch — the normal
	// case — stays quiet.
	if opts.Base != "" && (b.Name == opts.Base || b.Name == gitdata.LocalBranchName(opts.Base)) {
		return "default branch", false
	}
	if b.IsHead {
		return "checked out", true
	}
	// Checked out in another worktree: git refuses to delete it there just as
	// it does here, so it was never a real candidate.
	if b.Worktree != "" {
		return "checked out at " + b.Worktree, true
	}
	for _, pattern := range opts.Protect {
		ok, err := path.Match(pattern, b.Name)
		if err != nil {
			// An unusable pattern can't be evaluated, so keep the branch
			// rather than delete one the user meant to protect.
			return "unusable protect pattern " + pattern, true
		}
		if ok {
			return "protected by " + pattern, true
		}
	}
	return "", false
}
