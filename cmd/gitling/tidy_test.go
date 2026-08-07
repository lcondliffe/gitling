package main

import (
	"bytes"
	"flag"
	"io"
	"maps"
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

// TestTidySelection pins the rule the selector flags follow: --merged and
// --gone narrow, --stale adds. The additive half is the one that was built
// backwards first, so every combination is listed rather than a representative
// few — the cases that matter are precisely the ones easy to leave out.
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
		// The additive contract: --stale on its own must widen the default
		// selection, not replace it. If this ever returns {stale} alone, a
		// plain `gitling tidy --stale` silently stops offering to clean up the
		// merged branches it used to.
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
		// Bare --stale leaves the threshold at zero so tidy.Options falls back
		// to DefaultStaleAfter; it does not hard-code 90 days here.
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

// newTidyFlags wires the two custom flag types into a flag set the same way
// runTidy does, so these tests exercise them through the flag package rather
// than by calling Set directly.
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
		// The whole reason staleFlag exists: the flag package must accept
		// --stale with no value, which it only does for a boolean flag.
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
		// Being a boolean flag has a cost: a separated value is not consumed as
		// the flag's argument, it is left as a positional. This is the state
		// runTidy's adoption step exists to clean up, so pin it here — if the
		// flag package ever stops behaving this way, that code is dead and
		// this test says so.
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
		// Repeating a plain fs.String would keep only the last value; the point
		// of multiFlag is that every --protect glob survives.
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
