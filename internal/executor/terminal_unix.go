//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package executor

import (
	"os"
	"syscall"
	"unsafe"
)

func terminalGroup(file *os.File) (int32, error) {
	var group int32
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), uintptr(syscall.TIOCGPGRP), uintptr(unsafe.Pointer(&group)))
	if errno != 0 {
		return 0, errno
	}
	return group, nil
}

func restoreTerminalGroup(file *os.File, group int32) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), uintptr(syscall.TIOCSPGRP), uintptr(unsafe.Pointer(&group)))
	if errno != 0 {
		return errno
	}
	return nil
}
