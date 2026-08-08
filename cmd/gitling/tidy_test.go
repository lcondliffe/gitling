package main

import (
	"bytes"
	"flag"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lcondliffe/gitling/internal/tidy"
)

// reasons is shorthand for the set tidySelection is expected to produce.
func reasons(rs ...tidy.Reason) map[tidy.Reason]bool {
	m := map[tidy.Reason]bool{}
	for _, r := range rs {
		m[r] = true
	}
	return m
}

// TestTidySelection pins the selector-flag rule: --merged and --gone narrow,
// --stale adds.
func TestTidySelection(t *testing.T) {
	const day = 24 * time.Hour

	tests := []struct {
		name           string
		merged, gone   bool
		stale          staleFlag
		wantReasons    map[tidy.Reason]bool
		wantStaleAfter time.Duration
	}{{
		name:        "no flags selects the safe pair",
		wantReasons: reasons(tidy.ReasonMerged, tidy.ReasonGone),
	}, {
		name:        "merged narrows to merged",
		merged:      true,
		wantReasons: reasons(tidy.ReasonMerged),
	}, {
		name:        "gone narrows to gone",
		gone:        true,
		wantReasons: reasons(tidy.ReasonGone),
	}, {
		name:        "merged and gone together select both",
		merged:      true,
		gone:        true,
		wantReasons: reasons(tidy.ReasonMerged, tidy.ReasonGone),
	}, {
		// --stale alone must widen the default pair, not replace it.
		name:        "stale alone adds to the default pair",
		stale:       staleFlag{set: true},
		wantReasons: reasons(tidy.ReasonMerged, tidy.ReasonGone, tidy.ReasonStale),
	}, {
		name:        "stale adds to a narrowed merged selection",
		merged:      true,
		stale:       staleFlag{set: true},
		wantReasons: reasons(tidy.ReasonMerged, tidy.ReasonStale),
	}, {
		name:        "stale adds to a narrowed gone selection",
		gone:        true,
		stale:       staleFlag{set: true},
		wantReasons: reasons(tidy.ReasonGone, tidy.ReasonStale),
	}, {
		name:        "all three selectors",
		merged:      true,
		gone:        true,
		stale:       staleFlag{set: true},
		wantReasons: reasons(tidy.ReasonMerged, tidy.ReasonGone, tidy.ReasonStale),
	}, {
		// Zero threshold: tidy.Options falls back to DefaultStaleAfter.
		name:           "bare stale leaves the threshold to the default",
		stale:          staleFlag{set: true},
		wantReasons:    reasons(tidy.ReasonMerged, tidy.ReasonGone, tidy.ReasonStale),
		wantStaleAfter: 0,
	}, {
		name:           "stale with a value sets the threshold",
		stale:          staleFlag{set: true, value: "180d"},
		wantReasons:    reasons(tidy.ReasonMerged, tidy.ReasonGone, tidy.ReasonStale),
		wantStaleAfter: 180 * day,
	}, {
		name:           "stale value alongside a narrowing flag",
		merged:         true,
		stale:          staleFlag{set: true, value: "12w"},
		wantReasons:    reasons(tidy.ReasonMerged, tidy.ReasonStale),
		wantStaleAfter: 84 * day,
	}, {
		name:           "stale accepts the other duration units",
		stale:          staleFlag{set: true, value: "1y"},
		wantReasons:    reasons(tidy.ReasonMerged, tidy.ReasonGone, tidy.ReasonStale),
		wantStaleAfter: 365 * day,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tidySelection(tt.merged, tt.gone, tt.stale)
			if err != nil {
				t.Fatalf("tidySelection() error: %v", err)
			}
			if !maps.Equal(got.reasons, tt.wantReasons) {
				t.Errorf("reasons = %v, want %v", got.reasons, tt.wantReasons)
			}
			if got.staleAfter != tt.wantStaleAfter {
				t.Errorf("staleAfter = %v, want %v", got.staleAfter, tt.wantStaleAfter)
			}
		})
	}
}

func TestTidySelectionRejectsBadStaleValue(t *testing.T) {
	for _, v := range []string{"abc", "0d", "-5d", "5x"} {
		if _, err := tidySelection(false, false, staleFlag{set: true, value: v}); err == nil {
			t.Errorf("tidySelection(--stale=%q) = nil error, want error", v)
		}
	}
}

// newTidyFlags wires the custom flag types up the way runTidy does.
func newTidyFlags() (*flag.FlagSet, *staleFlag, *multiFlag) {
	fs := flag.NewFlagSet("tidy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	stale := &staleFlag{}
	protect := &multiFlag{}
	fs.Var(stale, "stale", "")
	fs.Var(protect, "protect", "")
	return fs, stale, protect
}

func TestStaleFlag(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantSet   bool
		wantValue string
		wantRest  []string
	}{{
		name:    "absent",
		args:    nil,
		wantSet: false,
	}, {
		name:      "bare",
		args:      []string{"--stale"},
		wantSet:   true,
		wantValue: "",
	}, {
		name:      "attached value",
		args:      []string{"--stale=180d"},
		wantSet:   true,
		wantValue: "180d",
	}, {
		// A boolean flag doesn't consume a separated value; runTidy adopts the
		// leftover positional.
		name:      "separated value is left as a positional",
		args:      []string{"--stale", "180d"},
		wantSet:   true,
		wantValue: "",
		wantRest:  []string{"180d"},
	}, {
		name:      "explicit true reads as bare",
		args:      []string{"--stale=true"},
		wantSet:   true,
		wantValue: "",
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs, stale, _ := newTidyFlags()
			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.args, err)
			}
			if stale.set != tt.wantSet {
				t.Errorf("set = %v, want %v", stale.set, tt.wantSet)
			}
			if stale.value != tt.wantValue {
				t.Errorf("value = %q, want %q", stale.value, tt.wantValue)
			}
			if got := fs.Args(); len(got) != len(tt.wantRest) {
				t.Errorf("remaining args = %q, want %q", got, tt.wantRest)
			} else {
				for i := range got {
					if got[i] != tt.wantRest[i] {
						t.Errorf("remaining args = %q, want %q", got, tt.wantRest)
						break
					}
				}
			}
			if got := stale.String(); got != tt.wantValue {
				t.Errorf("String() = %q, want %q", got, tt.wantValue)
			}
		})
	}
}

func TestStaleFlagIsBoolFlag(t *testing.T) {
	if !(&staleFlag{}).IsBoolFlag() {
		t.Error("staleFlag must report itself as a boolean flag, or --stale cannot be given bare")
	}
}

func TestMultiFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{{
		name: "absent collects nothing",
		args: nil,
		want: nil,
	}, {
		name: "single",
		args: []string{"--protect=release/*"},
		want: []string{"release/*"},
	}, {
		name: "repeated collects every value in order",
		args: []string{"--protect=release/*", "--protect", "wip/*", "--protect=spike"},
		want: []string{"release/*", "wip/*", "spike"},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs, _, protect := newTidyFlags()
			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.args, err)
			}
			if len(*protect) != len(tt.want) {
				t.Fatalf("protect = %q, want %q", *protect, tt.want)
			}
			for i, want := range tt.want {
				if (*protect)[i] != want {
					t.Errorf("protect[%d] = %q, want %q", i, (*protect)[i], want)
				}
			}
		})
	}
}

func TestMultiFlagString(t *testing.T) {
	m := multiFlag{"release/*", "wip/*"}
	if got := m.String(); got != "release/*,wip/*" {
		t.Errorf("String() = %q, want %q", got, "release/*,wip/*")
	}
	empty := multiFlag{}
	if got := empty.String(); got != "" {
		t.Errorf("empty String() = %q, want empty", got)
	}
}

func TestPluralBranches(t *testing.T) {
	for n, want := range map[int]string{0: "branches", 1: "branch", 2: "branches"} {
		if got := pluralBranches(n); got != want {
			t.Errorf("pluralBranches(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestRunTidyRejectsMalformedProtectGlob(t *testing.T) {
	var out bytes.Buffer
	if got := runTidy(&out, strings.NewReader(""), []string{"--no-fetch", "--protect", "[bad"}); got != 2 {
		t.Errorf("runTidy with a malformed glob = %d, want 2", got)
	}
}

func TestRunTidyAcceptsValidProtectGlob(t *testing.T) {
	var out bytes.Buffer
	if got := runTidy(&out, strings.NewReader(""), []string{"--no-fetch", "--protect", "release/*"}); got != 0 {
		t.Errorf("runTidy with a valid glob = %d, want 0", got)
	}
}

// TestRunTidyArgs covers the argument handling that sits above tidyRun. Every
// case here is a dry run against this repository, so nothing can be deleted.
func TestRunTidyArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{{
		// The separated --stale value is a positional the parser stopped at;
		// runTidy adopts it and resumes on what follows.
		name: "separated stale value is adopted",
		args: []string{"--no-fetch", "--stale", "180d", "--protect", "release/*"},
	}, {
		name: "attached stale value",
		args: []string{"--no-fetch", "--stale=180d"},
	}, {
		name: "bare stale",
		args: []string{"--no-fetch", "--stale"},
	}, {
		name: "no-color",
		args: []string{"--no-fetch", "--no-color"},
	}, {
		name: "a stray argument is rejected",
		args: []string{"--no-fetch", "nonsense"},
		want: 2,
	}, {
		// Not a duration, so it stays a stray argument rather than a threshold.
		name: "an unparseable stale value stays a stray argument",
		args: []string{"--no-fetch", "--stale", "soon"},
		want: 2,
	}, {
		name: "an invalid stale duration is rejected",
		args: []string{"--no-fetch", "--stale=0d"},
		want: 2,
	}, {
		name: "an unknown flag is rejected",
		args: []string{"--no-fetch", "--nope"},
		want: 2,
	}, {
		name: "an invalid color mode is rejected",
		args: []string{"--no-fetch", "--color=mauve"},
		want: 2,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if got := runTidy(&out, strings.NewReader(""), tt.args); got != tt.want {
				t.Errorf("runTidy(%q) = %d, want %d", tt.args, got, tt.want)
			}
		})
	}
}

// A config that can't be parsed stops the run: tidy is about to act on what it
// says, so guessing at the defaults instead would be acting on the wrong rules.
func TestRunTidyRejectsMalformedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gitling.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if got := runTidy(&out, strings.NewReader(""), []string{"--no-fetch", "--config", path}); got != 2 {
		t.Errorf("runTidy with a malformed config = %d, want 2", got)
	}
}

// TestProtectPatterns pins the merge tidy.Options is given: config globs first,
// then the run's own, and a pattern that can't be matched is an error rather
// than something Classify has to deal with per branch.
func TestProtectPatterns(t *testing.T) {
	tests := []struct {
		name             string
		config, flags    []string
		want             []string
		wantErrSubstring string
	}{{
		name: "neither",
	}, {
		name:   "config only",
		config: []string{"release/*"},
		want:   []string{"release/*"},
	}, {
		name:  "flags only",
		flags: []string{"wip/*"},
		want:  []string{"wip/*"},
	}, {
		name:   "config globs come first, both are kept",
		config: []string{"release/*", "hotfix/*"},
		flags:  []string{"wip/*"},
		want:   []string{"release/*", "hotfix/*", "wip/*"},
	}, {
		name:             "a malformed config glob is rejected",
		config:           []string{"[bad"},
		wantErrSubstring: "[bad",
	}, {
		name:             "a malformed flag glob is rejected",
		flags:            []string{"release/*", "[bad"},
		wantErrSubstring: "[bad",
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := protectPatterns(tt.config, tt.flags)
			if tt.wantErrSubstring != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSubstring) {
					t.Fatalf("error = %v, want one naming %q", err, tt.wantErrSubstring)
				}
				return
			}
			if err != nil {
				t.Fatalf("protectPatterns() error: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("protectPatterns() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A config-supplied protect glob has to reach tidy the same way --protect does.
func TestRunTidyUsesConfigProtect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gitling.json")
	if err := os.WriteFile(path, []byte(`{"protect":["[bad"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	// The glob is only ever validated on the way to tidy.Options, so rejecting
	// it proves the config's patterns take that path.
	if got := runTidy(&out, strings.NewReader(""), []string{"--no-fetch", "--config", path}); got != 2 {
		t.Errorf("runTidy with a malformed config glob = %d, want 2", got)
	}
}
