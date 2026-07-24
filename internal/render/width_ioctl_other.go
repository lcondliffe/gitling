//go:build !(linux || darwin || freebsd || netbsd || openbsd)

package render

import "os"

// ioctlWinsize has no implementation on platforms without a TIOCGWINSZ
// ioctl reachable from the stdlib syscall package (notably Windows). Width
// detection there falls back to the COLUMNS environment variable handled in
// TerminalWidth; without it, output stays unbounded (today's behavior).
func ioctlWinsize(f *os.File) (int, bool) {
	return 0, false
}
