//go:build !windows

package repl

import (
	"errors"
	"syscall"
)

func interruptedRead(err error) bool { return errors.Is(err, syscall.EINTR) }
