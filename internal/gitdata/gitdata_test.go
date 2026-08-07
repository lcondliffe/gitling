package gitdata

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseLog(t *testing.T) {
	const rs, us = "\x1e", "\x1f"
	sample := rs + "h1" + us + "Alice" + us + "a@x" + us + "1700000000" + us + "1700000001" + "\n" +
		"10\t2\tfile.go\n" +
		"-\t-\timg.png\n" + // binary file: counts are "-"
		rs + "h2" + us + "Bob" + us + "b@x" + us + "1700100000" + us + "1700100001" + "\n" +
		"3\t1\tpkg/{old.go => new.go}\n"

	got := parseLog(sample)
	if len(got) != 2 {
		t.Fatalf("got %d commits, want 2", len(got))
	}

	c0 := got[0]
	if c0.Hash != "h1" || c0.AuthorName != "Alice" || c0.AuthorEmail != "a@x" {
		t.Errorf("c0 header = %+v", c0)
	}
	if c0.Insertions != 10 || c0.Deletions != 2 {
		t.Errorf("c0 stats = +%d -%d, want +10 -2", c0.Insertions, c0.Deletions)
	}
	if !c0.AuthorTime.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("c0 AuthorTime = %v", c0.AuthorTime)
	}
	if want := []string{"file.go", "img.png"}; !equalStrings(c0.Files, want) {
		t.Errorf("c0 Files = %v, want %v", c0.Files, want)
	}

	c1 := got[1]
	if c1.Insertions != 3 || c1.Deletions != 1 {
		t.Errorf("c1 stats = +%d -%d, want +3 -1", c1.Insertions, c1.Deletions)
	}
	if want := []string{"pkg/new.go"}; !equalStrings(c1.Files, want) { // rename resolved
		t.Errorf("c1 Files = %v, want %v", c1.Files, want)
	}
}

func TestParseLogEmpty(t *testing.T) {
	if got := parseLog(""); len(got) != 0 {
		t.Errorf("parseLog(\"\") = %v, want empty", got)
	}
}

func TestCleanPath(t *testing.T) {
	cases := map[string]string{
		"normal.go":              "normal.go",
		"old.go => new.go":       "new.go",
		"pkg/{old.go => new.go}": "pkg/new.go",
		"a/{b => c}/d.go":        "a/c/d.go",
		"{ => new}/d.go":         "new/d.go",
	}
	for in, want := range cases {
		if got := cleanPath(in); got != want {
			t.Errorf("cleanPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseTrack(t *testing.T) {
	cases := []struct {
		in            string
		ahead, behind int
	}{
		{"", 0, 0},
		{"ahead 1", 1, 0},
		{"behind 2", 0, 2},
		{"ahead 3, behind 4", 3, 4},
		{"behind 5, ahead 6", 6, 5},
	}
	for _, c := range cases {
		a, b := parseTrack(c.in)
		if a != c.ahead || b != c.behind {
			t.Errorf("parseTrack(%q) = (%d, %d), want (%d, %d)", c.in, a, b, c.ahead, c.behind)
		}
	}
}

func TestParseBranches(t *testing.T) {
	const us = "\x1f"
	// HEAD marker, name, upstream, track, committerdate(unix), authorname
	sample := strings.Join([]string{"*", "main", "origin/main", "", "1700000000", "Ada"}, us) + "\n" +
		strings.Join([]string{" ", "feature", "origin/feature", "ahead 2, behind 1", "1700100000", "Alan"}, us) + "\n" +
		strings.Join([]string{" ", "stale", "origin/stale", "gone", "1700200000", "Grace"}, us) + "\n" +
		strings.Join([]string{" ", "local-only", "", "", "1700300000", "Linus"}, us) + "\n"

	got := parseBranches(sample)
	if len(got) != 4 {
		t.Fatalf("got %d branches, want 4", len(got))
	}

	main := got[0]
	if !main.IsHead || main.Name != "main" || main.Upstream != "origin/main" {
		t.Errorf("main = %+v", main)
	}
	if !main.HasCompare || main.Ahead != 0 || main.Behind != 0 || main.CompareRef != "origin/main" {
		t.Errorf("main should compare in-sync against upstream: %+v", main)
	}
	if !main.LastCommit.Equal(time.Unix(1700000000, 0)) || main.LastAuthor != "Ada" {
		t.Errorf("main tip = %v / %q", main.LastCommit, main.LastAuthor)
	}

	feature := got[1]
	if feature.IsHead || feature.Ahead != 2 || feature.Behind != 1 || !feature.HasCompare {
		t.Errorf("feature = %+v", feature)
	}

	stale := got[2]
	if !stale.Gone || stale.HasCompare {
		t.Errorf("stale should be gone with no comparison: %+v", stale)
	}

	local := got[3]
	if local.HasCompare || local.Gone || local.Upstream != "" {
		t.Errorf("local-only should have no upstream and no comparison yet: %+v", local)
	}
}

func TestParseRecentLog(t *testing.T) {
	const rs, us = "\x1e", "\x1f"
	rec := func(fields ...string) string { return rs + strings.Join(fields, us) }
	sample := rec("h1full", "h1", "Alice", "1700000000", "p1", "fix: land the thing (#18)", "") +
		rec("h2full", "h2", "Bob", "1700100000", "p1 p2", "Merge pull request #7 from bob/feat", "\nadd the widget\n")

	got := parseRecentLog(sample)
	if len(got) != 2 {
		t.Fatalf("got %d commits, want 2", len(got))
	}

	c0 := got[0]
	if c0.Hash != "h1full" || c0.Short != "h1" || c0.Author != "Alice" {
		t.Errorf("c0 header = %+v", c0)
	}
	if !c0.Time.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("c0 Time = %v", c0.Time)
	}
	if c0.Merge {
		t.Error("c0 has one parent, should not be a merge")
	}
	if c0.Subject != "fix: land the thing" || c0.PR != 18 {
		t.Errorf("c0 subject/PR = %q/%d, want %q/18", c0.Subject, c0.PR, "fix: land the thing")
	}

	c1 := got[1]
	if !c1.Merge {
		t.Error("c1 has two parents, should be a merge")
	}
	// The merge subject is boilerplate; the body's first line is the real title.
	if c1.Subject != "add the widget" || c1.PR != 7 {
		t.Errorf("c1 subject/PR = %q/%d, want %q/7", c1.Subject, c1.PR, "add the widget")
	}
}

func TestParseRecentLogEmpty(t *testing.T) {
	if got := parseRecentLog(""); len(got) != 0 {
		t.Errorf("parseRecentLog(\"\") = %v, want empty", got)
	}
}

func TestParseSubject(t *testing.T) {
	cases := []struct {
		name        string
		subject     string
		body        string
		wantSubject string
		wantPR      int
	}{
		{"squash merge", "fix: a thing (#18)", "", "fix: a thing", 18},
		{"gitlab squash", "fix: a thing (!42)", "", "fix: a thing", 42},
		{"multi-digit", "feat: x (#12345)", "", "feat: x", 12345},
		{"plain commit", "chore: tidy up", "", "chore: tidy up", 0},
		{"parenthetical, not a PR", "fix: handle (edge case)", "", "fix: handle (edge case)", 0},
		{"trailing hash, no digits", "fix: see (#)", "", "fix: see (#)", 0},
		{"non-numeric ref", "fix: see (#abc)", "", "fix: see (#abc)", 0},
		{"trailing junk after number", "fix: x (#12a)", "", "fix: x (#12a)", 0},
		// Stripping would leave nothing to show, so the subject is kept whole
		// and the number stays in it rather than being hoisted into its column.
		{"nothing but the ref", "(#18)", "", "(#18)", 0},
		{"merge commit with body title", "Merge pull request #7 from a/b", "\nadd the widget\n", "add the widget", 7},
		{"merge commit, empty body", "Merge pull request #7 from a/b", "", "Merge pull request #7 from a/b", 7},
		{"merge branch, no PR", "Merge branch 'main' into feat", "", "Merge branch 'main' into feat", 0},
		{"gitlab merge trailer", "Merge branch 'feat' into 'main'", "add widget\n\nSee merge request group/proj!42", "Merge branch 'feat' into 'main'", 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject, pr := parseSubject(tc.subject, tc.body)
			if subject != tc.wantSubject || pr != tc.wantPR {
				t.Errorf("parseSubject(%q, %q) = %q/%d, want %q/%d",
					tc.subject, tc.body, subject, pr, tc.wantSubject, tc.wantPR)
			}
		})
	}
}

func TestCountLines(t *testing.T) {
	cases := map[string]int{"": 0, "a\n": 1, "a\nb\n": 2, "a\nb": 2}
	for in, want := range cases {
		if got := countLines(in); got != want {
			t.Errorf("countLines(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseStatusCounts(t *testing.T) {
	// One file staged only, one modified only, one both, one untracked, one
	// conflicted, one deleted from the index.
	out := "M  staged.go\n" +
		" M modified.go\n" +
		"MM both.go\n" +
		"?? new.go\n" +
		"UU conflict.go\n" +
		"D  gone.go\n"

	staged, modified, untracked, conflicts := parseStatusCounts(out)
	// both.go counts once as staged and once as modified: the two overlap by
	// design, which is why only DirtyFiles is treated as a total.
	if staged != 3 {
		t.Errorf("staged = %d, want 3", staged)
	}
	if modified != 2 {
		t.Errorf("modified = %d, want 2", modified)
	}
	if untracked != 1 {
		t.Errorf("untracked = %d, want 1", untracked)
	}
	if conflicts != 1 {
		t.Errorf("conflicts = %d, want 1", conflicts)
	}
}

func TestParseStatusCountsEmpty(t *testing.T) {
	staged, modified, untracked, conflicts := parseStatusCounts("")
	if staged+modified+untracked+conflicts != 0 {
		t.Errorf("clean tree counted %d %d %d %d, want all zero", staged, modified, untracked, conflicts)
	}
}

func TestIsConflict(t *testing.T) {
	// Every unmerged combination git documents, plus near misses that are not.
	conflicted := []string{"DD", "AU", "UD", "UA", "DU", "AA", "UU"}
	for _, c := range conflicted {
		if !isConflict(c[0], c[1]) {
			t.Errorf("isConflict(%q) = false, want true", c)
		}
	}
	for _, c := range []string{"M ", " M", "MM", "??", "A ", "D ", "R "} {
		if isConflict(c[0], c[1]) {
			t.Errorf("isConflict(%q) = true, want false", c)
		}
	}
}

func TestParseStashList(t *testing.T) {
	// Newest first, the order `git stash list` uses.
	count, oldest := parseStashList("1700200000\n1700100000\n1700000000\n")
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
	if !oldest.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("oldest = %v, want %v", oldest, time.Unix(1700000000, 0))
	}
}

func TestParseStashListEmpty(t *testing.T) {
	count, oldest := parseStashList("")
	if count != 0 || !oldest.IsZero() {
		t.Errorf("parseStashList(\"\") = (%d, %v), want (0, zero time)", count, oldest)
	}
}

func TestParseBranchHealth(t *testing.T) {
	const us = "\x1f"
	now := time.Unix(1700000000, 0)
	old := now.AddDate(0, 0, -StaleBranchDays-1).Unix()
	recent := now.AddDate(0, 0, -1).Unix()

	line := func(name, track string, when int64) string {
		return strings.Join([]string{name, track, strconv.FormatInt(when, 10)}, us)
	}
	out := strings.Join([]string{
		line("main", "", recent),           // default branch: never a candidate
		line("feature", "ahead 2", recent), // live work
		line("old-feature", "", old),       // stale
		line("dropped", "gone", old),       // stale and gone
		line("dropped-2", "gone", recent),  // gone but recent
		line("legacy", "", old),            // stale
	}, "\n")

	skip := map[string]bool{"main": true}
	total, gone, stale := parseBranchHealth(out, now, skip)
	if total != 6 {
		t.Errorf("total = %d, want 6", total)
	}
	if gone != 2 {
		t.Errorf("gone = %d, want 2", gone)
	}
	if stale != 3 {
		t.Errorf("stale = %d, want 3", stale)
	}
}

// A branch old enough to be stale is still not a cleanup candidate when it is
// the one you are standing on, or the default branch.
func TestParseBranchHealthSkipsProtectedBranches(t *testing.T) {
	const us = "\x1f"
	now := time.Unix(1700000000, 0)
	old := strconv.FormatInt(now.AddDate(0, 0, -365).Unix(), 10)
	out := strings.Join([]string{"main", "gone", old}, us) + "\n" +
		strings.Join([]string{"wip", "gone", old}, us)

	total, gone, stale := parseBranchHealth(out, now, map[string]bool{"main": true, "wip": true})
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if gone != 0 || stale != 0 {
		t.Errorf("gone/stale = %d/%d, want 0/0", gone, stale)
	}
}

func TestCountBranchNames(t *testing.T) {
	skip := map[string]bool{"main": true}
	if got := countBranchNames("main\nfeature\nchore/tidy\n", skip); got != 2 {
		t.Errorf("countBranchNames = %d, want 2", got)
	}
	if got := countBranchNames("", skip); got != 0 {
		t.Errorf("countBranchNames(\"\") = %d, want 0", got)
	}
}

func TestLocalBranchName(t *testing.T) {
	cases := map[string]string{
		"origin/main":    "main",
		"main":           "main",
		"origin/release": "release",
		"":               "",
	}
	for in, want := range cases {
		if got := localBranchName(in); got != want {
			t.Errorf("localBranchName(%q) = %q, want %q", in, got, want)
		}
	}
}

func equalStrings(a, b []string) bool {
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
