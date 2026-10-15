// Package executor handles running external (non-built-in) commands.
// When the REPL receives a command name that is not in the registry, it
// delegates here.  The executor resolves the binary via PATH and wires up
// stdin / stdout / stderr so the child process behaves like an ordinary
// shell command.
package executor

import (
	"fmt"
	"os"
	"os/exec"
)

// ExecError is returned when an external command exits with a non-zero status.
type ExecError struct {
	Name     string
	ExitCode int
}

func (e *ExecError) Error() string {
	return fmt.Sprintf("command %q exited with code %d", e.Name, e.ExitCode)
}

// NotFoundError is returned when the command binary cannot be located.
type NotFoundError struct {
	Name string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("nexshell: command not found: %s", e.Name)
}

// Run executes name with args, inheriting the current process's
// stdin, stdout and stderr so interactive programs work correctly.
func Run(name string, args []string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return &NotFoundError{Name: name}
	}

	cmd := exec.Command(path, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return &ExecError{Name: name, ExitCode: exitErr.ExitCode()}
		}
		return fmt.Errorf("executor: %w", err)
	}
	return nil
}
