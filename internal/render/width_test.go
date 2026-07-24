package render

import (
	"os"
	"testing"
)

func TestTerminalWidthHonorsColumnsEnvVar(t *testing.T) {
	t.Setenv("COLUMNS", "132")
	got, ok := TerminalWidth(os.Stdout)
	if !ok || got != 132 {
		t.Fatalf("TerminalWidth() = (%d, %v), want (132, true)", got, ok)
	}
}

func TestTerminalWidthIgnoresInvalidColumnsEnvVar(t *testing.T) {
	for _, v := range []string{"", "0", "-10", "not-a-number"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("COLUMNS", v)
			// With an invalid/empty COLUMNS, TerminalWidth falls through to
			// the ioctl query; in this (non-terminal) test process that
			// should fail cleanly rather than return a bogus width.
			got, ok := TerminalWidth(os.Stdout)
			if ok && got <= 0 {
				t.Fatalf("TerminalWidth() = (%d, true), want a positive width or ok=false", got)
			}
		})
	}
}
