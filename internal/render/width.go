package render

import (
	"os"
	"strconv"
)

// DefaultWidth is a sensible fallback column count for callers that want to
// render *something* width-aware even when no width could be detected (e.g.
// deciding on a minimum viable layout). The renderers themselves treat
// Width == 0 as "unknown/unbounded" and skip width-dependent truncation
// entirely, so this constant is not applied inside the render package —
// it's here for callers (like cmd/gitling) that want one.
const DefaultWidth = 80

// TerminalWidth reports the display width of f: the COLUMNS environment
// variable if it is set to a positive integer (the POSIX convention, and
// what lets tests and scripts override detection), otherwise a
// platform-specific ioctl query. ok is false when neither source yields a
// usable width — piped/redirected output, or a platform without an ioctl
// implementation (e.g. Windows) and no COLUMNS set. Callers should treat
// !ok as "unknown", not substitute DefaultWidth, since 0 already means
// "unbounded" to the renderers in this package.
func TerminalWidth(f *os.File) (int, bool) {
	if v := os.Getenv("COLUMNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n, true
		}
	}
	return ioctlWinsize(f)
}
