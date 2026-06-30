package gitdata

import (
	"testing"
	"time"
)

func TestParseLog(t *testing.T) {
	const rs, us = "\x1e", "\x1f"
	sample := rs + "h1" + us + "Alice" + us + "a@x" + us + "1700000000" + us + "1700000001" + "\n" +
		"10\t2\tfile.go\n" +
		"-\t-\timg.png\n" + // binary file: counts are "-"
		rs + "h2" + us + "Bob" + us + "b@x" + us + "1700100000" + us + "1700100001" + "\n" +
		"3\t1\tpkg/{old.go => new.go}\n"

	got := parseLog(sample)
	if len(got) != 2 {
		t.Fatalf("got %d commits, want 2", len(got))
	}

	c0 := got[0]
	if c0.Hash != "h1" || c0.AuthorName != "Alice" || c0.AuthorEmail != "a@x" {
		t.Errorf("c0 header = %+v", c0)
	}
	if c0.Insertions != 10 || c0.Deletions != 2 {
		t.Errorf("c0 stats = +%d -%d, want +10 -2", c0.Insertions, c0.Deletions)
	}
	if !c0.AuthorTime.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("c0 AuthorTime = %v", c0.AuthorTime)
	}
	if want := []string{"file.go", "img.png"}; !equalStrings(c0.Files, want) {
		t.Errorf("c0 Files = %v, want %v", c0.Files, want)
	}

	c1 := got[1]
	if c1.Insertions != 3 || c1.Deletions != 1 {
		t.Errorf("c1 stats = +%d -%d, want +3 -1", c1.Insertions, c1.Deletions)
	}
	if want := []string{"pkg/new.go"}; !equalStrings(c1.Files, want) { // rename resolved
		t.Errorf("c1 Files = %v, want %v", c1.Files, want)
	}
}

func TestParseLogEmpty(t *testing.T) {
	if got := parseLog(""); len(got) != 0 {
		t.Errorf("parseLog(\"\") = %v, want empty", got)
	}
}

func TestCleanPath(t *testing.T) {
	cases := map[string]string{
		"normal.go":              "normal.go",
		"old.go => new.go":       "new.go",
		"pkg/{old.go => new.go}": "pkg/new.go",
		"a/{b => c}/d.go":        "a/c/d.go",
		"{ => new}/d.go":         "new/d.go",
	}
	for in, want := range cases {
		if got := cleanPath(in); got != want {
			t.Errorf("cleanPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCountLines(t *testing.T) {
	cases := map[string]int{"": 0, "a\n": 1, "a\nb\n": 2, "a\nb": 2}
	for in, want := range cases {
		if got := countLines(in); got != want {
			t.Errorf("countLines(%q) = %d, want %d", in, got, want)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
