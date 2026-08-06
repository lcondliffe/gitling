package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/lcondliffe/gitling/internal/gitdata"
	"github.com/lcondliffe/gitling/internal/render"
	"github.com/lcondliffe/gitling/internal/tidy"
)

// tidyOptions is one `gitling tidy` invocation's resolved configuration.
type tidyOptions struct {
	reasons    map[tidy.Reason]bool
	staleAfter time.Duration
	protect    []string
	apply      bool
	assumeYes  bool
	fetch      bool
	color      bool
	width      int
}

// runTidy parses the tidy subcommand's own flags and runs it. tidy gets a
// separate flag set from the dashboard views because it is the one command that
// writes: --apply next to --since on a shared flag set would invite exactly the
// kind of accident this command has to not have.
func runTidy(stdout io.Writer, stdin io.Reader, args []string) int {
	fs := flag.NewFlagSet("tidy", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = tidyUsage
	configFlag := fs.String("config", "", "path to config file")

	merged := fs.Bool("merged", false, "select branches merged into the default branch")
	gone := fs.Bool("gone", false, "select branches whose upstream has been deleted")
	stale := &staleFlag{}
	fs.Var(stale, "stale", "also select branches untouched for this long, even unmerged (default 90d when given without a value)")
	apply := fs.Bool("apply", false, "actually delete the branches (default: dry run)")
	assumeYes := fs.Bool("yes", false, "skip the confirmation prompt when applying")
	noFetch := fs.Bool("no-fetch", false, "skip the pruning fetch that refreshes which upstreams are gone")
	protect := multiFlag{}
	fs.Var(&protect, "protect", "glob of branch names never to delete (repeatable)")
	color := fs.String("color", "auto", "when to use color: always, never, auto")
	noColor := fs.Bool("no-color", false, "disable ANSI color output (alias for --color=never)")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	// --stale behaves like a boolean to the flag package so it can be given
	// bare, which means `--stale 180d` leaves the duration as a positional.
	// Adopt it, so the documented spelling works alongside `--stale=180d`.
	rest := fs.Args()
	if stale.set && stale.value == "" && len(rest) > 0 {
		if _, err := parseSinceDays(rest[0]); err == nil {
			stale.value = rest[0]
			// Flag parsing stopped at that positional, so resume on what
			// follows it — otherwise `--stale 180d --protect x` would treat
			// --protect as a stray argument.
			if err := fs.Parse(rest[1:]); err != nil {
				return 2
			}
			rest = fs.Args()
		}
	}
	if len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "gitling: tidy takes no arguments, got %q\n", rest[0])
		return 2
	}

	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	cfg, err := loadConfigForRun(*configFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitling:", err)
		return 2
	}

	if !explicit["color"] && cfg.Color != "" {
		*color = cfg.Color
	}
	if *noColor {
		*color = "never"
	}
	if err := validateColor(*color); err != nil {
		fmt.Fprintln(os.Stderr, "gitling:", err)
		return 2
	}

	sel, err := tidySelection(*merged, *gone, *stale)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitling:", err)
		return 2
	}

	opts := tidyOptions{
		reasons:    sel.reasons,
		staleAfter: sel.staleAfter,
		protect:    append(append([]string{}, cfg.Protect...), protect...),
		apply:      *apply,
		assumeYes:  *assumeYes,
		fetch:      !*noFetch,
		color:      colorEnabled(*color),
	}

	if width, ok := render.TerminalWidth(os.Stdout); ok {
		opts.width = width
	}

	if err := tidyRun(stdout, stdin, opts); err != nil {
		fmt.Fprintln(os.Stderr, "gitling:", err)
		return 1
	}
	return 0
}

// selection is the branch-selection half of tidyOptions: which categories are
// in play, and from what age a branch counts as stale.
type selection struct {
	reasons    map[tidy.Reason]bool
	staleAfter time.Duration
}

// tidySelection maps the three selector flags onto the categories Classify
// should consider. It is a function of its arguments alone so the rule below —
// the one that was built backwards the first time — can be tested by calling
// it rather than by running the binary and reading the output.
//
// --merged and --gone narrow the selection to what they name; --stale only ever
// adds, since "also clean up the old ones" should not quietly stop cleaning up
// the safe ones.
func tidySelection(merged, gone bool, stale staleFlag) (selection, error) {
	sel := selection{reasons: map[tidy.Reason]bool{}}
	if !merged && !gone {
		sel.reasons[tidy.ReasonMerged] = true
		sel.reasons[tidy.ReasonGone] = true
	}
	if merged {
		sel.reasons[tidy.ReasonMerged] = true
	}
	if gone {
		sel.reasons[tidy.ReasonGone] = true
	}
	// --stale is both a selector and a threshold: bare it means "the default 90
	// days", with a value it means that long.
	if stale.set {
		sel.reasons[tidy.ReasonStale] = true
		if stale.value != "" {
			days, err := parseSinceDays(stale.value)
			if err != nil {
				return selection{}, fmt.Errorf("invalid --stale %q (use d, w, mo, y)", stale.value)
			}
			sel.staleAfter = time.Duration(days) * 24 * time.Hour
		}
	}
	return sel, nil
}

func tidyRun(stdout io.Writer, stdin io.Reader, o tidyOptions) error {
	repo, err := gitdata.Open(".")
	if err != nil {
		return err
	}

	// Fetch first: Branch.Gone is derived from remote-tracking refs, so without
	// a pruning fetch a branch deleted on the forge months ago still looks
	// alive and the most useful category comes back empty. A failure here is
	// degraded, not fatal — being offline shouldn't stop you tidying up merged
	// branches — but it must be said out loud, because the plan that follows is
	// then built on stale information.
	if o.fetch {
		if err := repo.Fetch(true); err != nil {
			fmt.Fprintln(os.Stderr, "gitling: warning: fetch failed, working from local refs:", err)
			fmt.Fprintln(os.Stderr, "gitling: warning: branches deleted on the remote may not show as gone")
		}
	}

	branches, err := repo.Branches()
	if err != nil {
		return err
	}
	plan := tidy.Classify(branches, tidy.Options{
		Base:       repo.DefaultBranch(),
		Reasons:    o.reasons,
		StaleAfter: o.staleAfter,
		Protect:    o.protect,
		Now:        time.Now(),
	})

	model := render.TidyModel{Plan: plan, Now: time.Now(), Width: o.width}
	if !o.apply || plan.Empty() {
		render.Tidy(stdout, model, o.color)
		return nil
	}

	if !o.assumeYes {
		// Show the plan before asking, so the prompt is answered with the list
		// in view rather than from memory.
		model.Confirming = true
		render.Tidy(stdout, model, o.color)
		model.Confirming = false

		ok := confirm(stdout, stdin, fmt.Sprintf("Delete %d %s?",
			len(plan.Candidates), pluralBranches(len(plan.Candidates))))
		if !ok {
			fmt.Fprintln(stdout, "  aborted; nothing was deleted")
			fmt.Fprintln(stdout)
			return nil
		}
	}

	model.Applied = true
	model.Failed = deleteBranches(repo, plan)
	render.Tidy(stdout, model, o.color)
	return nil
}

// deleteBranches applies the plan, returning the branches that could not be
// deleted and why. One failure does not stop the rest: a branch git refuses is
// information, not a reason to leave the remaining cleanup half done.
func deleteBranches(repo *gitdata.Repo, plan tidy.Plan) map[string]error {
	failed := map[string]error{}
	for _, c := range plan.Candidates {
		if err := repo.DeleteBranch(c.Branch.Name, c.Force); err != nil {
			failed[c.Branch.Name] = err
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return failed
}

// confirm asks a yes/no question, defaulting to no. Anything that isn't an
// explicit yes — a bare newline, EOF because nothing is attached to stdin, an
// unreadable terminal — is a no, so the failure mode of every unusual situation
// is "nothing was deleted".
func confirm(stdout io.Writer, stdin io.Reader, question string) bool {
	fmt.Fprintf(stdout, "  %s [y/N] ", question)
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && line == "" {
		// No answer available at all; say so, since an unattended run that
		// meant to go through wants --yes rather than a silent no-op.
		fmt.Fprintln(stdout, "\n  no answer on stdin (pass --yes to skip this prompt)")
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func pluralBranches(n int) string {
	if n == 1 {
		return "branch"
	}
	return "branches"
}

// staleFlag is an optional-value flag: `--stale` selects the stale category at
// the default threshold, `--stale=180d` (or `--stale 180d`, adopted by the
// caller) sets a custom one. It reports itself as a boolean flag so the flag
// package accepts it bare.
type staleFlag struct {
	set   bool
	value string
}

func (s *staleFlag) IsBoolFlag() bool { return true }
func (s *staleFlag) String() string   { return s.value }

func (s *staleFlag) Set(v string) error {
	s.set = true
	// The flag package hands a bare --stale the literal "true".
	if v != "true" {
		s.value = v
	}
	return nil
}

// multiFlag collects a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func tidyUsage() {
	fmt.Fprint(os.Stderr, `gitling tidy - clean up local branches you're done with

Usage:
  gitling tidy [flags]

By default this is a dry run over the branches that are safe to drop: those
merged into the default branch, and those whose upstream has been deleted
(the shape a squash-merged pull request leaves behind). Nothing is deleted
until you pass --apply.

Flags:
  --merged         select only branches merged into the default branch
  --gone           select only branches whose upstream has been deleted
  --stale [dur]    also select branches untouched for dur, even unmerged
                    (default 90d; this is the category that can lose work)
  --apply          actually delete; prompts once unless --yes
  --yes            skip the confirmation prompt
  --no-fetch       skip the pruning fetch that refreshes gone-ness
  --protect <glob> never delete branches matching glob (repeatable)
  --color <mode>   when to use color: always, never, auto (default auto)
  --no-color       plain output with no ANSI escape codes

The current branch and the default branch are never deleted. Every branch
listed shows the commit it points at, so anything deleted can be restored
with `+"`git branch <name> <hash>`"+`.

Run inside a git repository.
`)
}
