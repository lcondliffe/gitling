package gitdata

import "testing"

// BenchmarkShellCommits benchmarks the shell-out backend's Commits("") (full
// history walk) against the repository containing this source tree. Run
// with: go test -bench BenchmarkShellCommits -benchtime 3x ./internal/gitdata
//
// The go-git equivalent (BenchmarkGogitCommits) lives in bench_gogit_test.go
// behind the `gogit` build tag; run both together with:
//
//	go test -tags gogit -bench . -benchtime 3x ./internal/gitdata
func BenchmarkShellCommits(b *testing.B) {
	r, err := openShell(".")
	if err != nil {
		b.Fatalf("openShell: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Commits(""); err != nil {
			b.Fatalf("Commits: %v", err)
		}
	}
}
