package aggregate

import (
	"testing"
	"time"
)

// 20 years of daily history: the case where the old per-sample map scan hurt.
func BenchmarkBuildGrowth20Years(b *testing.B) {
	a := New()
	now := time.Now()
	for i := 0; i < 7300; i++ {
		key := now.AddDate(0, 0, -i).Format(dayFmt)
		a.Days[key] = DayBucket{Commits: 1, Insertions: 30, Deletions: 10}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.BuildGrowth(now)
	}
}
