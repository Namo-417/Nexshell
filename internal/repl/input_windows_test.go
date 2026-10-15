//go:build windows

package repl

import (
	"os"
	"syscall"
	"testing"
)

func TestConsoleReadAbortRecovery(t *testing.T) {
	assertReadInterruptRecovery(t, &os.PathError{Op: "read", Path: "stdin", Err: syscall.ERROR_OPERATION_ABORTED})
}
