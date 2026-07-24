//go:build linux || darwin || freebsd || netbsd || openbsd

package render

import (
	"os"
	"syscall"
	"unsafe"
)

// winsize mirrors struct winsize from <sys/ioctl.h> / <asm/termios.h>: two
// uint16 dimensions in rows/cols, then two in pixels (unused here).
type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

// ioctlWinsize queries the kernel for f's window size via TIOCGWINSZ. It
// returns (0, false) when f isn't a terminal (piped/redirected output) or
// the ioctl otherwise fails.
func ioctlWinsize(f *os.File) (int, bool) {
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), tiocgwinsz, uintptr(unsafe.Pointer(&ws)))
	if errno != 0 || ws.Col == 0 {
		return 0, false
	}
	return int(ws.Col), true
}
