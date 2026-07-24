// Command gitling prints a compact, at-a-glance dashboard for the git repo in
// the current directory: repo vitals, an activity heatmap, top contributors, and
// codebase growth.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/lcondliffe/gitling/internal/aggregate"
	"github.com/lcondliffe/gitling/internal/cache"
	"github.com/lcondliffe/gitling/internal/gitdata"
	"github.com/lcondliffe/gitling/internal/render"
)

const defaultDays = 14 * 7 // default range: last 14 weeks

// version is overwritten at build time via -ldflags "-X main.version=..." in
// the release workflow. For `go install module@vX.Y.Z` builds (no ldflags), it
// falls back to the version Go stamps into the build info.
var version = "dev"

func buildVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

func main() {
	args := os.Args[1:]
	// Every drill-down named on the command line (subcommand or flag) is
	// collected here; asking for two different ones is an error.
	var requested []string
	// A drill-down may be named as the first positional (e.g. `gitling graph`).
	// Strip it before flag parsing so flags can follow the subcommand.
	if len(args) > 0 {
		if v, ok := subcommandView(args[0]); ok {
			requested = append(requested, v)
			args = args[1:]
		}
	}

	noColor := flag.Bool("no-color", false, "disable ANSI color output (alias for --color=never)")
	color := flag.String("color", "auto", "when to use color: always, never, auto (default auto)")
	since := flag.String("since", "", "time range for all sections: e.g. 30d, 12w, 6mo, 1y (default 14w)")
	graph := flag.Bool("graph", false, "show the full activity graph drill-down")
	churn := flag.Bool("churn", false, "show the full file churn drill-down")
	contributors := flag.Bool("contributors", false, "show the full contributor drill-down")
	branches := flag.Bool("branches", false, "show the branch overview drill-down")
	bucket := flag.String("bucket", "day", "activity graph bucket: day, week, month")
	dateBasis := flag.String("date", "author", "date basis for bucketing: author, commit")
	jsonOutput := flag.Bool("json", false, "emit machine-readable JSON instead of the human dashboard")
	showVersion := flag.Bool("version", false, "print version and exit")
	configFlag := flag.String("config", "", "path to config file (default $XDG_CONFIG_HOME/gitling/config.json or ~/.config/gitling/config.json)")
	flag.Usage = usage
	if err := flag.CommandLine.Parse(args); err != nil {
		os.Exit(2)
	}

	if *showVersion {
		fmt.Println("gitling", buildVersion())
		return
	}

	// Track which flags were explicitly passed on the command line, so config
	// file values only fill in ones the user left at their default.
	explicit := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	path, err := configPath(*configFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitling:", err)
		os.Exit(2)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitling:", err)
		os.Exit(2)
	}

	if !explicit["since"] && cfg.Since != "" {
		*since = cfg.Since
	}
	if !explicit["bucket"] && cfg.Bucket != "" {
		*bucket = cfg.Bucket
	}
	if !explicit["color"] && cfg.Color != "" {
		*color = cfg.Color
	}

	if *graph {
		requested = append(requested, "graph")
	}
	if *churn {
		requested = append(requested, "churn")
	}
	if *contributors {
		requested = append(requested, "contributors")
	}
	if *branches {
		requested = append(requested, "branches")
	}
	// A subcommand may also appear after flags (e.g. `gitling --since 1y churn`).
	if flag.NArg() > 0 {
		if v, ok := subcommandView(flag.Arg(0)); ok && flag.NArg() == 1 {
			requested = append(requested, v)
		} else {
			fmt.Fprintf(os.Stderr, "gitling: unexpected argument %q\n", flag.Arg(0))
			os.Exit(2)
		}
	}
	view, err := selectView(requested)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitling:", err)
		os.Exit(2)
	}
	if err := validateBucket(*bucket); err != nil {
		fmt.Fprintln(os.Stderr, "gitling:", err)
		os.Exit(2)
	}
	if err := validateDateBasis(*dateBasis); err != nil {
		fmt.Fprintln(os.Stderr, "gitling:", err)
		os.Exit(2)
	}
	// --no-color always wins over --color (explicit or from config): it is
	// the back-compat escape hatch and takes precedence when both are given.
	if *noColor {
		*color = "never"
	}
	if err := validateColor(*color); err != nil {
		fmt.Fprintln(os.Stderr, "gitling:", err)
		os.Exit(2)
	}

	width, ok := render.TerminalWidth(os.Stdout)
	if !ok {
		width = 0 // unknown/unbounded; renderers keep today's fixed-width behavior
	}

	if err := run(os.Stdout, *since, colorEnabled(*color), view, *bucket, aggregate.DateBasis(*dateBasis), *jsonOutput, width); err != nil {
		fmt.Fprintln(os.Stderr, "gitling:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `gitling - an at-a-glance git repository dashboard

Usage:
  gitling [flags]
  gitling graph [flags]
  gitling churn [flags]
  gitling contributors [flags]
  gitling branches [flags]

Flags:
  --since <dur>    time range for all sections: 30d, 12w, 6mo, 1y (default 14w)
  --graph          show the full activity graph drill-down
  --churn          show the full file churn drill-down
  --contributors   show the full contributor drill-down
  --branches       show the branch overview drill-down
  --bucket <b>     activity graph bucket: day, week, month (default day)
  --date <basis>   date basis for bucketing: author, commit (default author)
  --json           emit machine-readable JSON instead of the human dashboard
  --color <mode>   when to use color: always, never, auto (default auto)
  --no-color       plain output with no ANSI escape codes (alias for --color=never)
  --config <path>  path to config file (default $XDG_CONFIG_HOME/gitling/config.json
                    or ~/.config/gitling/config.json; $GITLING_CONFIG overrides)
  --version        print version and exit

Config file (optional, JSON) may set defaults for "since", "color", and
"bucket"; command-line flags always override it. --no-color overrides both.

Run inside a git repository.
`)
}

func run(stdout io.Writer, since string, color bool, view, bucket string, dateBasis aggregate.DateBasis, jsonOutput bool, width int) error {
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

	// The branches view is live git state, independent of the commit-history
	// aggregate, so serve it before the (potentially expensive) history walk.
	if !jsonOutput && view == "branches" {
		branches, err := repo.Branches()
		if err != nil {
			return err
		}
		render.Branches(stdout, render.BranchesModel{Branches: branches, Now: now, Width: width}, color)
		return nil
	}

	store := cache.New(gitDir, dateBasis)
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
			agg.Merge(commits, dateBasis)
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
		Width:      width,
	}
	m.Days = agg.DailyCounts(sinceTime, now)
	m.TotalCommits = aggregate.TotalCommits(m.Days)
	m.Streak = aggregate.Streak(m.Days)
	buckets := aggregate.BucketCounts(m.Days, bucket)
	if !jsonOutput && view == "graph" {
		render.Graph(stdout, render.GraphModel{
			RangeLabel:   m.RangeLabel,
			Bucket:       bucket,
			Days:         m.Days,
			Buckets:      buckets,
			TotalCommits: m.TotalCommits,
			Streak:       m.Streak,
			Now:          now,
			Width:        width,
		}, color)
		return nil
	}
	if !jsonOutput && view == "churn" {
		render.Churn(stdout, render.ChurnModel{
			RangeLabel: m.RangeLabel,
			Files:      agg.HotFiles(sinceTime, now, 0), // 0 == all files
			Now:        now,
			Width:      width,
		}, color)
		return nil
	}
	if !jsonOutput && view == "contributors" {
		render.Contributors(stdout, render.ContributorsModel{
			RangeLabel:   m.RangeLabel,
			Contributors: agg.TopContributors(sinceTime, now, 0), // 0 == all authors
			Now:          now,
			Width:        width,
		}, color)
		return nil
	}

	m.Contributors = agg.TopContributors(sinceTime, now, 5)
	m.HotFiles = agg.HotFiles(sinceTime, now, 3)
	m.Growth = agg.BuildGrowth(now)
	if jsonOutput {
		return render.JSON(stdout, m, bucket, buckets)
	}

	render.Dashboard(stdout, m, color)
	return nil
}

// selectView reduces the drill-down views named on the command line (as a
// subcommand, a flag, or both) to a single view. Naming the same view twice
// (e.g. `gitling --graph graph`) is harmless; naming two different views is
// ambiguous and rejected.
func selectView(requested []string) (string, error) {
	view := "dashboard"
	for _, v := range requested {
		if view != "dashboard" && v != view {
			return "", fmt.Errorf("conflicting views %q and %q requested; pick one", view, v)
		}
		view = v
	}
	return view, nil
}

// subcommandView maps a drill-down subcommand name to its view identifier.
func subcommandView(name string) (string, bool) {
	switch name {
	case "graph":
		return "graph", true
	case "churn":
		return "churn", true
	case "contributors":
		return "contributors", true
	case "branches":
		return "branches", true
	default:
		return "", false
	}
}

func validateBucket(bucket string) error {
	switch bucket {
	case "day", "week", "month":
		return nil
	default:
		return fmt.Errorf("invalid --bucket %q (use day, week, or month)", bucket)
	}
}

func validateDateBasis(basis string) error {
	if !aggregate.DateBasis(basis).Valid() {
		return fmt.Errorf("invalid --date %q (use author or commit)", basis)
	}
	return nil
}

// validateColor checks that mode is one of the supported --color values.
func validateColor(mode string) error {
	switch mode {
	case "always", "never", "auto":
		return nil
	default:
		return fmt.Errorf("invalid --color %q (use always, never, or auto)", mode)
	}
}

// colorEnabled implements --color's three modes. "always" forces color on
// (useful when piping into a pager or a screenshot renderer, where stdout
// isn't a TTY but ANSI is still wanted); "never" forces it off; "auto" (the
// default) honors the NO_COLOR convention and auto-disables color when
// stdout is not a terminal (piped or redirected).
func colorEnabled(mode string) bool {
	switch mode {
	case "always":
		return true
	case "never":
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
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
