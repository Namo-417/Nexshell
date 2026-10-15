// Package executor runs parsed foreground commands, pipelines and redirections.
// Parsing is deliberately independent of process and file management.
package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nexshell/internal/parser"
	"nexshell/internal/registry"
)

// ExecError describes a completed command with a nonzero status. Its diagnostic
// has already been written, if appropriate, to that stage's standard error.
type ExecError struct {
	Name     string
	ExitCode int
	Err      error
}

func (e *ExecError) Error() string {
	return fmt.Sprintf("command %q exited with code %d", e.Name, e.ExitCode)
}
func (e *ExecError) Unwrap() error { return e.Err }

// NotFoundError distinguishes a failed command lookup (status 127).
type NotFoundError struct{ Name string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("nexshell: command not found: %s", e.Name)
}

// StartError describes a located executable that could not start (status 126).
type StartError struct {
	Name string
	Err  error
}

func (e *StartError) Error() string { return fmt.Sprintf("nexshell: start %s: %v", e.Name, e.Err) }
func (e *StartError) Unwrap() error { return e.Err }

// RedirectionError describes a failed input/output file open (status 1).
type RedirectionError struct {
	Name, Target string
	Err          error
}

func (e *RedirectionError) Error() string {
	return fmt.Sprintf("nexshell: %s: redirect %q: %v", e.Name, e.Target, e.Err)
}
func (e *RedirectionError) Unwrap() error { return e.Err }

// Execute runs the complete plan. It returns zero on success, the rightmost
// nonzero stage status on command failure (pipefail), or 130 on cancellation.
// It owns command diagnostics; callers must not print returned errors again.
// A registry.ExitError is returned unchanged for the REPL to handle.
// Arbitrary custom input readers must unblock their Read when canceled; the
// shell uses files, and WaitDelay bounds os/exec's output-copy shutdown.
func Execute(ctx context.Context, plan parser.Plan, shell registry.Context) (status int, resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return 130, err
	}
	if len(plan.Commands) == 0 {
		return 0, nil
	}

	// Built-ins and exec's copy goroutines can share a custom output writer.
	// File streams remain files, preserving direct terminal/pipe inheritance.
	var outputMu sync.Mutex
	shell.Stdin = shell.Input()
	shell.Stdout = synchronized(shell.Output(), &outputMu)
	shell.Stderr = synchronized(shell.ErrorOutput(), &outputMu)
	shell.Context = ctx
	dir := ""
	if shell.CWD != nil {
		dir = *shell.CWD
	}

	if err := parser.Validate(plan); err != nil {
		fmt.Fprintln(shell.ErrorOutput(), err)
		return 2, err
	}
	stages, status, failed, err := prepare(plan, shell, dir)
	if err != nil {
		if diagnosticErr := preparationDiagnostic(plan.Commands[failed], shell, dir, err); diagnosticErr != nil {
			return 1, diagnosticErr
		}
		return status, err
	}
	var files []*os.File
	defer func() {
		for _, file := range files {
			if closeErr := file.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
				fmt.Fprintf(shell.ErrorOutput(), "nexshell: close redirection: %v\n", closeErr)
				if resultErr == nil {
					status, resultErr = 1, closeErr
				}
			}
		}
	}()
	// All lookups and pipeline-safety checks precede opening output files.
	for i := range stages {
		for _, redir := range plan.Commands[i].Redirections {
			target := resolvePath(dir, redir.Target)
			f, openErr := openRedirection(redir.Kind, target)
			err = openErr
			if err != nil {
				err = &RedirectionError{Name: stages[i].name, Target: redir.Target, Err: err}
				fmt.Fprintln(stages[i].shell.ErrorOutput(), err)
				return 1, err
			}
			files = append(files, f)
			switch redir.Kind {
			case parser.Input:
				stages[i].shell.Stdin = f
			case parser.Output, parser.Append:
				stages[i].shell.Stdout = f
			case parser.Stderr:
				stages[i].shell.Stderr = f
			}
		}
		// The original cat requires operands unless input was explicitly
		// redirected or piped. Preserve native inherited-input semantics for
		// built-ins while allowing custom input readers supplied by callers.
		if i == 0 && stages[i].builtin != nil && stages[i].shell.Stdin == os.Stdin {
			stages[i].shell.Stdin = nil
		}
	}

	var pipes []*os.File
	defer func() { closeFiles(pipes) }()
	for i := 0; i+1 < len(stages); i++ {
		r, w, pipeErr := os.Pipe()
		if pipeErr != nil {
			fmt.Fprintf(shell.ErrorOutput(), "nexshell: pipe: %v\n", pipeErr)
			return 1, pipeErr
		}
		pipes = append(pipes, r, w)
		stages[i].shell.Stdout = w
		stages[i].pipeEnds = append(stages[i].pipeEnds, w)
		stages[i+1].shell.Stdin = r
		stages[i+1].pipeEnds = append(stages[i+1].pipeEnds, r)
	}

	processes, err := newProcessSet(shell.Input())
	if err != nil {
		fmt.Fprintf(shell.ErrorOutput(), "nexshell: process management: %v\n", err)
		return 126, err
	}
	defer func() {
		if closeErr := processes.close(); closeErr != nil {
			fmt.Fprintf(shell.ErrorOutput(), "nexshell: process cleanup: %v\n", closeErr)
			if resultErr == nil {
				status, resultErr = 1, closeErr
			}
		}
	}()
	results := make(chan stageResult, len(stages))
	var started []*exec.Cmd
	for i := range stages {
		stage := &stages[i]
		if stage.builtin != nil {
			continue
		}
		cmd := exec.Command(stage.path, plan.Commands[i].Args...)
		cmd.Dir = dir
		cmd.Stdin = stage.shell.Input()
		cmd.Stdout = stage.shell.Output()
		cmd.Stderr = stage.shell.ErrorOutput()
		cmd.WaitDelay = 250 * time.Millisecond
		err = processes.prepare(cmd)
		stage.cmd = cmd
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = cmd.Start()
			if err == nil {
				started = append(started, cmd)
				err = processes.track(cmd)
			}
		}
		if err != nil {
			closeFiles(pipes)
			processes.kill()
			for _, command := range started {
				_ = command.Wait()
			}
			if ctx.Err() != nil {
				return 130, ctx.Err()
			}
			err = &StartError{Name: stage.name, Err: err}
			fmt.Fprintln(stage.shell.ErrorOutput(), err)
			return 126, err
		}
		// Child owns duplicated handles: retaining the parent's pipe handles
		// would prevent EOF and broken-pipe propagation during Wait.
		closeFiles(stage.pipeEnds)
	}
	// Keep an exited Unix group leader unreaped until every process has
	// joined its group; otherwise a very short first stage can erase it.
	for i := range stages {
		if command := stages[i].cmd; command != nil {
			go func(index int, cmd *exec.Cmd) {
				results <- stageResult{index: index, err: cmd.Wait()}
			}(i, command)
		}
	}

	for i := range stages {
		stage := &stages[i]
		if stage.builtin == nil {
			continue
		}
		go func(index int, s *preparedStage) {
			defer closeFiles(s.pipeEnds)
			results <- stageResult{index: index, err: s.builtin.Run(s.shell, plan.Commands[index].Args)}
		}(i, stage)
	}

	stageErrors := make([]error, len(stages))
	canceled := false
	done := ctx.Done()
	for remaining := len(stages); remaining > 0; {
		select {
		case result := <-results:
			stageErrors[result.index] = result.err
			remaining--
			var exit *exec.ExitError
			if errors.As(result.err, &exit) && processInterrupted(exit) {
				canceled = true
				done = nil
				cancel()
				closeFiles(pipes)
				closeFiles(files)
				processes.kill()
			}
		case <-done:
			canceled = true
			done = nil
			closeFiles(pipes)
			closeFiles(files)
			processes.kill()
		}
	}
	if canceled || ctx.Err() != nil {
		if ctx.Err() != nil {
			return 130, ctx.Err()
		}
		return 130, context.Canceled
	}
	status = 0
	for i, stageErr := range stageErrors {
		if stageErr == nil {
			continue
		}
		var exit *registry.ExitError
		if errors.As(stageErr, &exit) {
			return exit.Code, exit
		}
		code := 1
		if stages[i].builtin != nil {
			fmt.Fprintln(stages[i].shell.ErrorOutput(), stageErr)
		} else {
			var exited *exec.ExitError
			if errors.As(stageErr, &exited) {
				code = processExitCode(exited)
				fmt.Fprintf(stages[i].shell.ErrorOutput(), "nexshell: %s: exit status %d\n", stages[i].name, code)
			} else {
				fmt.Fprintf(stages[i].shell.ErrorOutput(), "nexshell: %s: %v\n", stages[i].name, stageErr)
			}
		}
		status = code
		resultErr = &ExecError{Name: stages[i].name, ExitCode: code, Err: stageErr}
	}
	return status, resultErr
}

type preparedStage struct {
	name     string
	path     string
	builtin  registry.Command
	cmd      *exec.Cmd
	shell    registry.Context
	pipeEnds []*os.File
}

type stageResult struct {
	index int
	err   error
}

func prepare(plan parser.Plan, shell registry.Context, dir string) ([]preparedStage, int, int, error) {
	stages := make([]preparedStage, len(plan.Commands))
	for i, command := range plan.Commands {
		stages[i] = preparedStage{name: command.Name, shell: shell}
		if shell.Registry != nil {
			stages[i].builtin, _ = shell.Registry.Lookup(command.Name)
		}
		if builtin := stages[i].builtin; builtin != nil {
			if len(stages) > 1 {
				safe, ok := builtin.(registry.PipelineCommand)
				if !ok || !safe.PipelineSafe() {
					return nil, 2, i, fmt.Errorf("nexshell: %s cannot run in a pipeline because it may change shell state", command.Name)
				}
			}
			continue
		}
		name := command.Name
		if strings.ContainsAny(name, `/\\`) && !filepath.IsAbs(name) && dir != "" {
			name = filepath.Join(dir, name)
		}
		path, err := exec.LookPath(name)
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
				return nil, 127, i, &NotFoundError{Name: command.Name}
			}
			return nil, 126, i, &StartError{Name: command.Name, Err: err}
		}
		stages[i].path = path
	}
	return stages, 0, 0, nil
}

// Lookup must precede opening stdout files, but a stage's explicit stderr
// destination still receives its lookup diagnostic. Failure to open that file
// is itself reported to the shell's inherited stderr.
func preparationDiagnostic(command parser.Command, shell registry.Context, dir string, err error) error {
	for _, redirect := range command.Redirections {
		if redirect.Kind != parser.Stderr {
			continue
		}
		file, openErr := openRedirection(parser.Stderr, resolvePath(dir, redirect.Target))
		if openErr != nil {
			redirectErr := &RedirectionError{Name: command.Name, Target: redirect.Target, Err: openErr}
			fmt.Fprintln(shell.ErrorOutput(), redirectErr)
			return redirectErr
		}
		defer file.Close()
		fmt.Fprintln(file, err)
		return nil
	}
	fmt.Fprintln(shell.ErrorOutput(), err)
	return nil
}

func openRedirection(kind parser.Kind, target string) (*os.File, error) {
	info, err := os.Stat(target)
	if err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("only regular files are supported as redirection targets")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	switch kind {
	case parser.Input:
		return os.Open(target)
	case parser.Output, parser.Stderr:
		return os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o666)
	case parser.Append:
		return os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	}
	return nil, fmt.Errorf("unsupported redirection kind %d", kind)
}

func resolvePath(dir, name string) string {
	if dir == "" || filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(dir, name)
}

func closeFiles(files []*os.File) {
	for _, file := range files {
		_ = file.Close()
	}
}

type lockedWriter struct {
	writer io.Writer
	mu     *sync.Mutex
}

func (w lockedWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(data)
}

func synchronized(w io.Writer, mu *sync.Mutex) io.Writer {
	if _, ok := w.(*os.File); ok {
		return w
	}
	return lockedWriter{writer: w, mu: mu}
}

// Run retains the Phase 1 external-command interface.
func Run(name string, args []string) error {
	_, err := Execute(context.Background(), parser.Plan{Commands: []parser.Command{{Name: name, Args: args}}}, registry.Context{})
	return err
}
