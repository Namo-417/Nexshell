//go:build !windows && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !aix

package executor

import (
	"fmt"
	"io"
	"os/exec"
	"runtime"
)

type processSet struct{}

func newProcessSet(_ io.Reader) (*processSet, error) {
	return nil, fmt.Errorf("foreground process management is unsupported on %s", runtime.GOOS)
}
func (s *processSet) prepare(cmd *exec.Cmd) error { return nil }
func (s *processSet) track(cmd *exec.Cmd) error   { return nil }
func (s *processSet) kill()                       {}
func (s *processSet) close() error                { return nil }
func processExitCode(err *exec.ExitError) int     { return err.ExitCode() }
func processInterrupted(err *exec.ExitError) bool { return false }
