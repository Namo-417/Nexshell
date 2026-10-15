//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || aix

package executor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// External stages share one foreground group so terminal-generated interrupts
// reach every stage. The executor does not reap the leader until all stages
// have joined, preserving the group even if the first command exits quickly.
type processSet struct {
	group      int
	input      *os.File
	terminal   *os.File
	foreground int32
}

func newProcessSet(input io.Reader) (*processSet, error) {
	file, _ := input.(*os.File)
	return &processSet{input: file}, nil
}

func (s *processSet) prepare(cmd *exec.Cmd) error {
	attributes := &syscall.SysProcAttr{Setpgid: true, Pgid: s.group}
	if s.group == 0 {
		if input := s.input; input != nil {
			foreground, err := terminalGroup(input)
			if err == nil {
				if foreground != int32(syscall.Getpgrp()) {
					return fmt.Errorf("shell does not own the input terminal's foreground process group")
				}
				// Go performs this handoff in the child with signals blocked,
				// before exec and before a terminal read can trigger SIGTTIN.
				attributes.Foreground = true
				attributes.Ctty = int(input.Fd())
				s.terminal, s.foreground = input, foreground
			} else if !errors.Is(err, syscall.ENOTTY) {
				return fmt.Errorf("foreground terminal: %w", err)
			}
		}
	}
	cmd.SysProcAttr = attributes
	return nil
}

func (s *processSet) track(cmd *exec.Cmd) error {
	if s.group == 0 {
		s.group = cmd.Process.Pid
	}
	return nil
}

func (s *processSet) kill() {
	if s.group != 0 {
		_ = syscall.Kill(-s.group, syscall.SIGKILL)
	}
}

func (s *processSet) close() error {
	s.kill()
	if s.terminal == nil {
		return nil
	}
	// The shell is temporarily a background terminal group. POSIX permits
	// tcsetpgrp from it only while SIGTTOU is ignored or blocked. No child is
	// started during restoration, and the previous ignored state is retained.
	ignored := signal.Ignored(syscall.SIGTTOU)
	signal.Ignore(syscall.SIGTTOU)
	if !ignored {
		defer signal.Reset(syscall.SIGTTOU)
	}
	if err := restoreTerminalGroup(s.terminal, s.foreground); err != nil {
		return fmt.Errorf("restore foreground terminal: %w", err)
	}
	return nil
}

func processExitCode(err *exec.ExitError) int {
	if status, ok := err.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return err.ExitCode()
}

func processInterrupted(err *exec.ExitError) bool {
	status, ok := err.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGINT
}
