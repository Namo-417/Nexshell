//go:build aix || solaris

package executor

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
)

// These platforms expose terminal ioctls only through libc in the standard
// library. Regular files, pipes and /dev/null remain usable; reject other
// character-device input explicitly rather than creating a stopped child.
func terminalGroup(file *os.File) (int32, error) {
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return 0, syscall.ENOTTY
	}
	if nullInfo, err := os.Stat(os.DevNull); err == nil && os.SameFile(info, nullInfo) {
		return 0, syscall.ENOTTY
	}
	return 0, fmt.Errorf("terminal input is unsupported on %s", runtime.GOOS)
}

func restoreTerminalGroup(*os.File, int32) error { return nil }
