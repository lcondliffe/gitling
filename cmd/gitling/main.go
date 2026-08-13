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
	"github.com/lcondliffe/gitling/internal/forge"
	"github.com/lcondliffe/gitling/internal/gitdata"
	"github.com/lcondliffe/gitling/internal/render"
)

const defaultDays = 14 * 7 // default range: last 14 weeks

// defaultRecent is how many recent commits the dashboard lists by default:
// enough to see what just landed without crowding out the other panels.
const defaultRecent = 5

// maxPRs caps the open pull requests listed; the panel is an "is anything
// waiting on me" glance, not a queue.
const maxPRs = 5

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
	// tidy is handled apart from the dashboard views: it is the only subcommand
	// that writes, so it gets its own flag set rather than sharing one with the
	// read-only views (see runTidy). It also loads the config itself, since it
	// needs the protect list that nothing else uses. Checked before any
	// drill-down is stripped, so `gitling graph tidy` stays an error rather
	// than silently running the mutating command.
	if len(args) > 0 && args[0] == "tidy" {
		os.Exit(runTidy(os.Stdout, os.Stdin, args[1:]))
	}

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
	recent := flag.Int("recent", defaultRecent, "number of recent commits to list on the dashboard (0 hides the panel)")
	layout := flag.String("layout", "auto", "dashboard layout: auto, wide, stack")
	bucket := flag.String("bucket", "day", "activity graph bucket: day, week, month")
	dateBasis := flag.String("date", "author", "date basis for bucketing: author, commit")
	jsonOutput := flag.Bool("json", false, "emit machine-readable JSON instead of the human dashboard")
	prs := flag.Bool("prs", true, "show open pull requests (needs the forge CLI, e.g. gh)")
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
	if !explicit["recent"] && cfg.Recent != nil {
		*recent = *cfg.Recent
	}
	if !explicit["layout"] && cfg.Layout != "" {
		*layout = cfg.Layout
	}
	if !explicit["prs"] && cfg.PRs != nil {
		*prs = *cfg.PRs
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
	if *recent < 0 {
		fmt.Fprintf(os.Stderr, "gitling: invalid --recent %d (must be 0 or more)\n", *recent)
		os.Exit(2)
	}
	if !render.ValidLayout(*layout) {
		fmt.Fprintf(os.Stderr, "gitling: invalid --layout %q (use auto, wide, or stack)\n", *layout)
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

	if err := run(os.Stdout, options{
		since:     *since,
		color:     colorEnabled(*color),
		view:      view,
		bucket:    *bucket,
		dateBasis: aggregate.DateBasis(*dateBasis),
		json:      *jsonOutput,
		recent:    *recent,
		layout:    *layout,
		width:     width,
		prs:       *prs,
	}); err != nil {
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
  gitling tidy [flags]

Flags:
  --since <dur>    time range for all sections: 30d, 12w, 6mo, 1y (default 14w)
  --graph          show the full activity graph drill-down
  --churn          show the full file churn drill-down
  --contributors   show the full contributor drill-down
  --branches       show the branch overview drill-down
  --recent <n>     recent commits listed on the dashboard, 0 hides them (default 5)
  --layout <mode>  dashboard layout: auto, wide, stack (default auto)
  --bucket <b>     activity graph bucket: day, week, month (default day)
  --date <basis>   date basis for bucketing: author, commit (default author)
  --json           emit machine-readable JSON instead of the human dashboard
  --prs=false      skip the open pull requests panel (needs a forge CLI: gh)
  --color <mode>   when to use color: always, never, auto (default auto)
  --no-color       plain output with no ANSI escape codes (alias for --color=never)
  --config <path>  path to config file (default $XDG_CONFIG_HOME/gitling/config.json
                    or ~/.config/gitling/config.json; $GITLING_CONFIG overrides)
  --version        print version and exit

The tidy subcommand cleans up local branches you're done with and takes its
own flags; run "gitling tidy --help" for those. It is a dry run unless you
pass --apply, and it is the only subcommand that changes anything.

Config file (optional, JSON) may set defaults for "since", "color", "bucket",
"recent", "layout", and "prs"; command-line flags always override it.
--no-color overrides both.

Run inside a git repository.
`)
}

// options is one run's resolved configuration, after command-line flags, the
// config file, and terminal detection have all been folded together.
type options struct {
	since     string
	color     bool
	view      string
	bucket    string
	dateBasis aggregate.DateBasis
	json      bool
	recent    int
	layout    string
	width     int  // terminal columns; 0 when unknown (piped or redirected)
	prs       bool // list open pull requests on the dashboard
}

func run(stdout io.Writer, o options) error {
	// JSON only covers the dashboard model. Say so rather than silently
	// serving dashboard data to someone who asked for a drill-down.
	if o.json && o.view != "dashboard" {
		return fmt.Errorf("--json is only available for the dashboard view, not %s", o.view)
	}

	repo, err := gitdata.Open(".")
	if err != nil {
		return err
	}
	gitDir, err := repo.GitDir()
	if err != nil {
		return err
	}

	vitals := repo.Vitals()

	days, err := parseSinceDays(o.since)
	if err != nil {
		return err
	}
	now := time.Now()
	sinceTime := now.AddDate(0, 0, -days)

	// The branches view is live git state, independent of the commit-history
	// aggregate, so serve it before the (potentially expensive) history walk.
	if o.view == "branches" {
		branches, err := repo.Branches()
		if err != nil {
			return err
		}
		render.Branches(stdout, render.BranchesModel{Branches: branches, Now: now, Width: o.width}, o.color)
		return nil
	}

	// Open PRs come from the forge's CLI over the network, so the fetch runs
	// alongside the history walk instead of in front of it. The channel is
	// closed (yielding nil on receive) when the panel is off.
	prs := make(chan []forge.PR, 1)
	if o.prs && o.view == "dashboard" {
		remote := repo.RemoteURL() // resolved here: Repo isn't used concurrently
		go func() { prs <- forge.List(".", remote, maxPRs) }()
	} else {
		close(prs)
	}

	store := cache.New(gitDir, o.dateBasis)
	agg, lastHash, ok := store.Load()
	if !ok {
		agg = aggregate.New()
	}

	// Only walk history when there are commits. An empty repo renders vitals
	// plus empty panels.
	head, headErr := repo.Head()
	if headErr == nil {
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
			agg.Merge(commits, o.dateBasis)
			if err := store.Save(agg, head); err != nil {
				// Cache is an optimization, not correctness; warn and continue.
				fmt.Fprintln(os.Stderr, "gitling: warning: cache write failed:", err)
			}
		}
	}

	m := render.Model{
		Vitals:     vitals,
		RangeLabel: rangeLabel(o.since),
		Now:        now,
		Width:      o.width,
		Layout:     o.layout,
	}
	m.Days = agg.DailyCounts(sinceTime, now)
	m.TotalCommits = aggregate.TotalCommits(m.Days)
	m.Streak = aggregate.Streak(m.Days)
	buckets := aggregate.BucketCounts(m.Days, o.bucket)
	if o.view == "graph" {
		render.Graph(stdout, render.GraphModel{
			RangeLabel:   m.RangeLabel,
			Bucket:       o.bucket,
			Days:         m.Days,
			Buckets:      buckets,
			TotalCommits: m.TotalCommits,
			Streak:       m.Streak,
			Now:          now,
			Width:        o.width,
		}, o.color)
		return nil
	}
	if o.view == "churn" {
		render.Churn(stdout, render.ChurnModel{
			RangeLabel: m.RangeLabel,
			Files:      agg.HotFiles(sinceTime, now, 0), // 0 == all files
			Now:        now,
			Width:      o.width,
		}, o.color)
		return nil
	}
	if o.view == "contributors" {
		render.Contributors(stdout, render.ContributorsModel{
			RangeLabel:   m.RangeLabel,
			Contributors: agg.TopContributors(sinceTime, now, 0), // 0 == all authors
			Now:          now,
			Width:        o.width,
		}, o.color)
		return nil
	}

	m.Contributors = agg.TopContributors(sinceTime, now, 5)
	m.HotFiles = agg.HotFiles(sinceTime, now, 3)
	m.Growth = agg.BuildGrowth(now)
	// Recent commits are live git state (and ignore --since: "what landed last"
	// is only useful unfiltered), so they come straight from the log rather than
	// the cached aggregate. Skipped on an empty repo, where there is no HEAD.
	if o.recent > 0 && headErr == nil {
		commits, err := repo.RecentCommits(o.recent)
		if err != nil {
			return err
		}
		m.Recent = commits
	}
	m.PRs = <-prs
	if o.json {
		return render.JSON(stdout, m, o.bucket, buckets)
	}

	render.Dashboard(stdout, m, o.color)
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
