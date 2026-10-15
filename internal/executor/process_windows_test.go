//go:build windows

package executor

import (
	"syscall"
	"testing"
)

func assertProcessStopped(t *testing.T, pid int) {
	t.Helper()
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err == syscall.Errno(87) {
		return
	} // process no longer exists
	if err != nil {
		t.Fatalf("OpenProcess(%d): %v", pid, err)
	}
	defer syscall.CloseHandle(handle)
	state, err := syscall.WaitForSingleObject(handle, 5000)
	if err != nil || state != syscall.WAIT_OBJECT_0 {
		t.Fatalf("process %d survived cleanup: state=%d err=%v", pid, state, err)
	}
}
