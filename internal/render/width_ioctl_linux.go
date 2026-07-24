//go:build linux

package render

// tiocgwinsz is the Linux ioctl request number for TIOCGWINSZ
// (asm-generic/ioctls.h); it differs from the BSD/Darwin value.
const tiocgwinsz = 0x5413
