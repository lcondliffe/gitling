package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

func TestParseSinceDays(t *testing.T) {
	ok := map[string]int{
		"":       defaultDays,
		"30d":    30,
		"30days": 30,
		"12w":    84,
		"2weeks": 14,
		"6mo":    180,
		"1y":     365,
		"90":     90,
	}
	for in, want := range ok {
		got, err := parseSinceDays(in)
		if err != nil {
			t.Errorf("parseSinceDays(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseSinceDays(%q) = %d, want %d", in, got, want)
		}
	}

	for _, in := range []string{"abc", "0d", "-5d", "5x", "d"} {
		if _, err := parseSinceDays(in); err == nil {
			t.Errorf("parseSinceDays(%q) = nil error, want error", in)
		}
	}
}

func TestRangeLabel(t *testing.T) {
	if got := rangeLabel(""); got != "last 14 weeks" {
		t.Errorf("rangeLabel(\"\") = %q", got)
	}
	if got := rangeLabel("30d"); got != "last 30d" {
		t.Errorf("rangeLabel(30d) = %q", got)
	}
}

func TestSubcommandView(t *testing.T) {
	views := map[string]string{"graph": "graph", "churn": "churn", "contributors": "contributors", "branches": "branches"}
	for name, want := range views {
		got, ok := subcommandView(name)
		if !ok || got != want {
			t.Errorf("subcommandView(%q) = (%q, %v), want (%q, true)", name, got, ok, want)
		}
	}
	for _, name := range []string{"dashboard", "", "GRAPH", "BRANCHES"} {
		if got, ok := subcommandView(name); ok {
			t.Errorf("subcommandView(%q) = (%q, true), want (_, false)", name, got)
		}
	}
}

func TestSelectView(t *testing.T) {
	ok := []struct {
		requested []string
		want      string
	}{
		{nil, "dashboard"},
		{[]string{"graph"}, "graph"},
		{[]string{"churn"}, "churn"},
		{[]string{"churn", "churn"}, "churn"}, // flag + subcommand naming the same view
	}
	for _, c := range ok {
		got, err := selectView(c.requested)
		if err != nil || got != c.want {
			t.Errorf("selectView(%v) = (%q, %v), want (%q, nil)", c.requested, got, err, c.want)
		}
	}
	for _, requested := range [][]string{
		{"graph", "churn"},
		{"churn", "graph"},
		{"graph", "contributors"},
		{"branches", "graph"},
	} {
		if got, err := selectView(requested); err == nil {
			t.Errorf("selectView(%v) = (%q, nil), want error", requested, got)
		}
	}
}

func TestValidateBucket(t *testing.T) {
	for _, in := range []string{"day", "week", "month"} {
		if err := validateBucket(in); err != nil {
			t.Errorf("validateBucket(%q) error: %v", in, err)
		}
	}
	if err := validateBucket("quarter"); err == nil {
		t.Fatal("validateBucket(quarter) = nil error, want error")
	}
}

func TestValidateColor(t *testing.T) {
	for _, in := range []string{"always", "never", "auto"} {
		if err := validateColor(in); err != nil {
			t.Errorf("validateColor(%q) error: %v", in, err)
		}
	}
	for _, in := range []string{"", "sometimes", "ALWAYS"} {
		if err := validateColor(in); err == nil {
			t.Errorf("validateColor(%q) = nil error, want error", in)
		}
	}
}

func TestColorEnabled(t *testing.T) {
	if !colorEnabled("always") {
		t.Error(`colorEnabled("always") = false, want true`)
	}
	if colorEnabled("never") {
		t.Error(`colorEnabled("never") = true, want false`)
	}
	// "auto" delegates to NO_COLOR / TTY detection; just confirm it doesn't
	// panic and that NO_COLOR forces it off regardless of TTY state.
	t.Setenv("NO_COLOR", "1")
	if colorEnabled("auto") {
		t.Error(`colorEnabled("auto") with NO_COLOR set = true, want false`)
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()

	// Missing file is not an error.
	missing := filepath.Join(dir, "missing.json")
	cfg, err := loadConfig(missing)
	if err != nil {
		t.Fatalf("loadConfig(missing) error: %v", err)
	}
	if cfg != (config{}) {
		t.Errorf("loadConfig(missing) = %+v, want zero value", cfg)
	}

	// Valid config parses.
	valid := filepath.Join(dir, "config.json")
	if err := os.WriteFile(valid, []byte(`{"since":"30d","color":"always","bucket":"week"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadConfig(valid)
	if err != nil {
		t.Fatalf("loadConfig(valid) error: %v", err)
	}
	want := config{Since: "30d", Color: "always", Bucket: "week"}
	if cfg != want {
		t.Errorf("loadConfig(valid) = %+v, want %+v", cfg, want)
	}

	// Unknown keys are ignored.
	extra := filepath.Join(dir, "extra.json")
	if err := os.WriteFile(extra, []byte(`{"since":"7d","panels":["graph"],"nonsense":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadConfig(extra)
	if err != nil {
		t.Fatalf("loadConfig(extra) error: %v", err)
	}
	if cfg.Since != "7d" {
		t.Errorf("loadConfig(extra).Since = %q, want %q", cfg.Since, "7d")
	}

	// Malformed JSON is a clear error.
	malformed := filepath.Join(dir, "malformed.json")
	if err := os.WriteFile(malformed, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(malformed); err == nil {
		t.Error("loadConfig(malformed) = nil error, want error")
	}
}

func TestConfigPath(t *testing.T) {
	t.Run("explicit flag wins", func(t *testing.T) {
		got, err := configPath("/explicit/path.json")
		if err != nil || got != "/explicit/path.json" {
			t.Errorf("configPath(flag) = (%q, %v), want (/explicit/path.json, nil)", got, err)
		}
	})

	t.Run("GITLING_CONFIG env wins over XDG", func(t *testing.T) {
		t.Setenv("GITLING_CONFIG", "/env/path.json")
		t.Setenv("XDG_CONFIG_HOME", "/xdg")
		got, err := configPath("")
		if err != nil || got != "/env/path.json" {
			t.Errorf("configPath(env) = (%q, %v), want (/env/path.json, nil)", got, err)
		}
	})

	t.Run("XDG_CONFIG_HOME used when set", func(t *testing.T) {
		t.Setenv("GITLING_CONFIG", "")
		t.Setenv("XDG_CONFIG_HOME", "/xdg")
		got, err := configPath("")
		want := filepath.Join("/xdg", "gitling", "config.json")
		if err != nil || got != want {
			t.Errorf("configPath(xdg) = (%q, %v), want (%q, nil)", got, err, want)
		}
	})

	t.Run("falls back to home dir", func(t *testing.T) {
		t.Setenv("GITLING_CONFIG", "")
		t.Setenv("XDG_CONFIG_HOME", "")
		home := t.TempDir()
		t.Setenv("HOME", home)        // os.UserHomeDir reads HOME on Unix...
		t.Setenv("USERPROFILE", home) // ...and USERPROFILE on Windows.
		got, err := configPath("")
		want := filepath.Join(home, ".config", "gitling", "config.json")
		if err != nil || got != want {
			t.Errorf("configPath(home) = (%q, %v), want (%q, nil)", got, err, want)
		}
	})
}

// TestFlagOverridesConfigPrecedence exercises the explicit-flag-vs-config
// precedence logic the same way main() does: parse flags into a fresh
// FlagSet, use Visit to see what the user actually passed, and confirm only
// unset flags are filled in from config.
func TestFlagOverridesConfigPrecedence(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"since":"1y","color":"never","bucket":"month"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	since := fs.String("since", "", "")
	color := fs.String("color", "auto", "")
	bucket := fs.String("bucket", "day", "")
	if err := fs.Parse([]string{"--since", "7d"}); err != nil {
		t.Fatal(err)
	}

	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	if !explicit["since"] && cfg.Since != "" {
		*since = cfg.Since
	}
	if !explicit["bucket"] && cfg.Bucket != "" {
		*bucket = cfg.Bucket
	}
	if !explicit["color"] && cfg.Color != "" {
		*color = cfg.Color
	}

	// --since was explicit: flag value wins over config.
	if *since != "7d" {
		t.Errorf("since = %q, want %q (flag should override config)", *since, "7d")
	}
	// --bucket and --color were not passed: config fills them in.
	if *bucket != "month" {
		t.Errorf("bucket = %q, want %q (config should fill unset flag)", *bucket, "month")
	}
	if *color != "never" {
		t.Errorf("color = %q, want %q (config should fill unset flag)", *color, "never")
	}
}
