package main

import "testing"

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
