//go:build darwin || freebsd || netbsd || openbsd

package render

// tiocgwinsz is the BSD-family (including Darwin) ioctl request number for
// TIOCGWINSZ (sys/ttycom.h); it differs from the Linux value.
const tiocgwinsz = 0x40087468
