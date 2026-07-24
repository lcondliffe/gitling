//go:build gogit

package gitdata

import "testing"

// BenchmarkGogitCommits is the go-git counterpart to BenchmarkShellCommits
// (bench_test.go), run against the same repository. Run with:
//
//	go test -tags gogit -bench . -benchtime 3x ./internal/gitdata
func BenchmarkGogitCommits(b *testing.B) {
	g, err := openGogit(".")
	if err != nil {
		b.Fatalf("openGogit: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.Commits(""); err != nil {
			b.Fatalf("Commits: %v", err)
		}
	}
}
