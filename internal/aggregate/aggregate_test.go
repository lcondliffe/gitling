package aggregate

import (
	"testing"
	"time"

	"github.com/lcondliffe/gitling/internal/gitdata"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 12, 0, 0, 0, time.Local) // noon avoids DST edges
}

func commit(name, email string, t time.Time, ins, del int, files ...string) gitdata.Commit {
	return gitdata.Commit{
		AuthorName: name, AuthorEmail: email, AuthorTime: t,
		Insertions: ins, Deletions: del, Files: files,
	}
}

func TestDailyCountsAndTotal(t *testing.T) {
	a := New()
	a.Merge([]gitdata.Commit{
		commit("A", "a@x", day(2024, 6, 2), 1, 0, "f.go"),
		commit("A", "a@x", day(2024, 6, 2), 1, 0, "g.go"),
	})
	days := a.DailyCounts(day(2024, 6, 1), day(2024, 6, 3))
	if len(days) != 3 {
		t.Fatalf("got %d days, want 3", len(days))
	}
	want := []int{0, 2, 0}
	for i, d := range days {
		if d.Count != want[i] {
			t.Errorf("day %d count = %d, want %d", i, d.Count, want[i])
		}
	}
	if got := TotalCommits(days); got != 2 {
		t.Errorf("TotalCommits = %d, want 2", got)
	}
}

func TestStreak(t *testing.T) {
	mk := func(counts ...int) []DayCount {
		out := make([]DayCount, len(counts))
		for i, c := range counts {
			out[i] = DayCount{Count: c}
		}
		return out
	}
	cases := []struct {
		name string
		in   []DayCount
		want int
	}{
		{"trailing active", mk(0, 1, 1, 1), 3},
		{"empty today allowed", mk(1, 1, 0), 2},
		{"gap before today breaks", mk(1, 0, 1), 1},
		{"all empty", mk(0, 0, 0), 0},
		{"empty today then gap", mk(1, 0, 0), 0},
	}
	for _, tc := range cases {
		if got := Streak(tc.in); got != tc.want {
			t.Errorf("%s: Streak = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestTopContributorsCoalescesByName(t *testing.T) {
	a := New()
	a.Merge([]gitdata.Commit{
		commit("Luke", "luke@personal", day(2024, 6, 2), 1, 0),
		commit("Luke", "luke@work", day(2024, 6, 3), 1, 0), // same name, 2nd email
		commit("Bob", "bob@x", day(2024, 6, 3), 1, 0),
	})
	got := a.TopContributors(day(2024, 6, 1), day(2024, 6, 30), 5)
	if len(got) != 2 {
		t.Fatalf("got %d contributors, want 2 (Luke merged): %+v", len(got), got)
	}
	if got[0].Name != "Luke" || got[0].Commits != 2 {
		t.Errorf("top = %+v, want Luke with 2 commits", got[0])
	}
	if got[1].Name != "Bob" || got[1].Commits != 1 {
		t.Errorf("second = %+v, want Bob with 1 commit", got[1])
	}
}

func TestTopContributorsRangeFilter(t *testing.T) {
	a := New()
	a.Merge([]gitdata.Commit{
		commit("A", "a@x", day(2024, 1, 1), 1, 0), // out of range
		commit("A", "a@x", day(2024, 6, 2), 1, 0), // in range
	})
	got := a.TopContributors(day(2024, 6, 1), day(2024, 6, 30), 5)
	if len(got) != 1 || got[0].Commits != 1 {
		t.Errorf("range filter failed: %+v", got)
	}
}

func TestHotFiles(t *testing.T) {
	a := New()
	a.Merge([]gitdata.Commit{
		commit("A", "a@x", day(2024, 6, 2), 1, 0, "hot.go", "cold.go"),
		commit("A", "a@x", day(2024, 6, 3), 1, 0, "hot.go"),
	})
	got := a.HotFiles(day(2024, 6, 1), day(2024, 6, 30), 3)
	if len(got) != 2 || got[0].Path != "hot.go" || got[0].Commits != 2 {
		t.Fatalf("HotFiles = %+v, want hot.go(2) first", got)
	}
}

func TestHotFilesDedupWithinCommit(t *testing.T) {
	a := New()
	// A file listed twice in one commit must count once for that commit.
	a.Merge([]gitdata.Commit{
		commit("A", "a@x", day(2024, 6, 2), 1, 0, "dup.go", "dup.go"),
	})
	got := a.HotFiles(day(2024, 6, 1), day(2024, 6, 30), 3)
	if len(got) != 1 || got[0].Commits != 1 {
		t.Errorf("HotFiles dedup = %+v, want dup.go(1)", got)
	}
}

func TestBuildGrowth(t *testing.T) {
	a := New()
	a.Merge([]gitdata.Commit{
		commit("A", "a@x", day(2023, 12, 1), 100, 0), // before 6mo baseline
		commit("A", "a@x", day(2024, 6, 1), 60, 10),  // within last 6mo: net +50
	})
	until := day(2024, 7, 1) // baseline = 2024-01-01
	g := a.BuildGrowth(day(2024, 6, 1), until)

	if g.TotalLOC != 150 {
		t.Errorf("TotalLOC = %d, want 150", g.TotalLOC)
	}
	if !g.HasPct {
		t.Fatal("HasPct = false, want true (data exists before baseline)")
	}
	if g.Pct < 49.9 || g.Pct > 50.1 { // (150-100)/100
		t.Errorf("Pct = %.2f, want ~50", g.Pct)
	}
	if len(g.Spark) == 0 {
		t.Fatal("Spark is empty")
	}
	if last := g.Spark[len(g.Spark)-1]; last != 150 {
		t.Errorf("Spark end = %d, want 150 (cumulative LOC at until)", last)
	}
}

func TestBuildGrowthNoBaseline(t *testing.T) {
	a := New()
	a.Merge([]gitdata.Commit{
		commit("A", "a@x", day(2024, 6, 1), 100, 0), // only recent data
	})
	g := a.BuildGrowth(day(2024, 6, 1), day(2024, 7, 1))
	if g.HasPct {
		t.Errorf("HasPct = true, want false (no pre-6mo baseline)")
	}
	if g.TotalLOC != 100 {
		t.Errorf("TotalLOC = %d, want 100", g.TotalLOC)
	}
}

func TestMergeIsAdditive(t *testing.T) {
	a := New()
	a.Merge([]gitdata.Commit{commit("A", "a@x", day(2024, 6, 2), 1, 0)})
	a.Merge([]gitdata.Commit{commit("A", "a@x", day(2024, 6, 2), 1, 0)}) // incremental batch
	days := a.DailyCounts(day(2024, 6, 2), day(2024, 6, 2))
	if days[0].Count != 2 {
		t.Errorf("after two merges count = %d, want 2", days[0].Count)
	}
}
