// Command gitling prints a compact, at-a-glance dashboard for the git repo in
// the current directory: repo vitals, an activity heatmap, top contributors, and
// codebase growth.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lcondliffe/gitling/internal/aggregate"
	"github.com/lcondliffe/gitling/internal/cache"
	"github.com/lcondliffe/gitling/internal/gitdata"
	"github.com/lcondliffe/gitling/internal/render"
)

const defaultDays = 14 * 7 // default range: last 14 weeks

// version is overwritten at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	noColor := flag.Bool("no-color", false, "disable ANSI color output")
	since := flag.String("since", "", "time range for all sections: e.g. 30d, 12w, 6mo, 1y (default 14w)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Println("gitling", version)
		return
	}

	// Positional args are reserved for future drill-down subcommands
	// (--graph, --churn, --contributors, --branches); reject them for now.
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "gitling: unexpected argument %q (subcommands are not implemented in v0.1)\n", flag.Arg(0))
		os.Exit(2)
	}

	if err := run(os.Stdout, *since, colorEnabled(*noColor)); err != nil {
		fmt.Fprintln(os.Stderr, "gitling:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `gitling - an at-a-glance git repository dashboard

Usage:
  gitling [flags]

Flags:
  --since <dur>   time range for all sections: 30d, 12w, 6mo, 1y (default 14w)
  --no-color      plain output with no ANSI escape codes
  --version       print version and exit

Run inside a git repository.
`)
}

func run(stdout io.Writer, since string, color bool) error {
	repo, err := gitdata.Open(".")
	if err != nil {
		return err
	}
	gitDir, err := repo.GitDir()
	if err != nil {
		return err
	}

	vitals, _ := repo.Vitals()

	days, err := parseSinceDays(since)
	if err != nil {
		return err
	}
	now := time.Now()
	sinceTime := now.AddDate(0, 0, -days)

	store := cache.New(gitDir)
	agg, lastHash, ok := store.Load()
	if !ok {
		agg = aggregate.New()
	}

	// Only walk history when there are commits. An empty repo renders vitals
	// plus empty panels.
	if head, err := repo.Head(); err == nil {
		revRange := "" // empty == full history
		switch {
		case ok && lastHash == head:
			// Cache already current; nothing to walk.
		case ok && repo.IsAncestor(lastHash, head):
			revRange = lastHash + "..HEAD" // incremental: only new commits
		default:
			// No cache, or history was rewritten under us: rebuild fresh.
			agg = aggregate.New()
		}

		if !(ok && lastHash == head) {
			commits, err := repo.Commits(revRange)
			if err != nil {
				return err
			}
			agg.Merge(commits)
			if err := store.Save(agg, head); err != nil {
				// Cache is an optimization, not correctness; warn and continue.
				fmt.Fprintln(os.Stderr, "gitling: warning: cache write failed:", err)
			}
		}
	}

	m := render.Model{
		Vitals:     vitals,
		RangeLabel: rangeLabel(since),
		Now:        now,
	}
	m.Days = agg.DailyCounts(sinceTime, now)
	m.TotalCommits = aggregate.TotalCommits(m.Days)
	m.Streak = aggregate.Streak(m.Days)
	m.Contributors = agg.TopContributors(sinceTime, now, 5)
	m.HotFiles = agg.HotFiles(sinceTime, now, 3)
	m.Growth = agg.BuildGrowth(sinceTime, now)

	render.Dashboard(stdout, m, color)
	return nil
}

// colorEnabled honors --no-color and the NO_COLOR convention, and auto-disables
// color when stdout is not a terminal (piped or redirected).
func colorEnabled(noColor bool) bool {
	if noColor || os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// parseSinceDays converts a duration like "30d", "12w", "6mo", "1y" into a whole
// number of days. An empty string yields the 14-week default.
func parseSinceDays(s string) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return defaultDays, nil
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid --since %q", s)
	}
	switch s[i:] {
	case "", "d", "day", "days":
		return n, nil
	case "w", "wk", "week", "weeks":
		return n * 7, nil
	case "mo", "month", "months":
		return n * 30, nil
	case "y", "yr", "year", "years":
		return n * 365, nil
	default:
		return 0, fmt.Errorf("invalid --since unit in %q (use d, w, mo, y)", s)
	}
}

func rangeLabel(since string) string {
	if s := strings.TrimSpace(since); s != "" {
		return "last " + s
	}
	return "last 14 weeks"
}
