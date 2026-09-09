package render

import (
	"os"
	"syscall"
	"unsafe"
)

var getConsoleScreenBufferInfo = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleScreenBufferInfo")

// Windows reports a buffer and a visible window; use the latter (inclusive).
func terminalSize(f *os.File) (rows, cols int, ok bool) {
	var info struct {
		Size, Cursor struct{ X, Y int16 }
		Attributes   uint16
		Window       struct{ Left, Top, Right, Bottom int16 }
		Maximum      struct{ X, Y int16 }
	}
	result, _, _ := getConsoleScreenBufferInfo.Call(f.Fd(), uintptr(unsafe.Pointer(&info)))
	if result == 0 {
		return 0, 0, false
	}
	return int(info.Window.Bottom-info.Window.Top) + 1, int(info.Window.Right-info.Window.Left) + 1, true
}

func ioctlWinsize(f *os.File) (int, bool) {
	_, cols, ok := terminalSize(f)
	return cols, ok && cols > 0
}
