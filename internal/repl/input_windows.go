//go:build windows

package repl

import (
	"errors"
	"syscall"
)

// Ctrl+C can abort ReadConsole as well as deliver an interrupt signal.
func interruptedRead(err error) bool {
	return errors.Is(err, syscall.ERROR_OPERATION_ABORTED) || errors.Is(err, syscall.EINTR)
}
