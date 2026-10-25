// Package repl implements NexShell's foreground command loop.
package repl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"nexshell/internal/alias"
	"nexshell/internal/executor"
	"nexshell/internal/history"
	"nexshell/internal/parser"
	"nexshell/internal/registry"
	"nexshell/internal/task"
)

// Shell holds the state of one foreground shell session.
type Shell struct {
	cwd     string
	reg     *registry.Registry
	aliases *alias.Store
	hist    *history.Store
}

func New(reg *registry.Registry, aliases *alias.Store, hist *history.Store) (*Shell, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("repl: cannot get working directory: %w", err)
	}
	task.SetExecute(executor.Execute)
	return &Shell{cwd: cwd, reg: reg, aliases: aliases, hist: hist}, nil
}

// Run handles Ctrl+C, restores signal handling on return, and returns the shell
// exit status. main owns os.Exit so deferred cleanup can finish first.
func (s *Shell) Run() int {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	defer signal.Stop(signals)
	return s.run(context.Background(), os.Stdin, os.Stdout, os.Stderr, signals)
}

// RunContext runs with caller-owned streams. Cancellation stops the session;
// interactive interrupts in Run cancel only the active foreground pipeline.
// A caller that supplies a blocking reader must unblock it when cancelling.
func (s *Shell) RunContext(ctx context.Context, in io.Reader, out, errOut io.Writer) int {
	return s.run(ctx, in, out, errOut, nil)
}

type lineResult struct {
	line string
	err  error
}

func (s *Shell) run(ctx context.Context, in io.Reader, out, errOut io.Writer, signals <-chan os.Signal) (status int) {
	if err := s.hist.Load(); err != nil {
		fmt.Fprintf(errOut, "nexshell: warning: could not load history: %v\n", err)
	}
	defer func() {
		if err := s.hist.Save(); err != nil {
			fmt.Fprintf(errOut, "nexshell: could not save history: %v\n", err)
			if status == 0 {
				status = 1
			}
		}
	}()
	for {
		if ctx.Err() != nil {
			return 130
		}
		if err := s.printPrompt(out); err != nil {
			fmt.Fprintf(errOut, "nexshell: prompt: %v\n", err)
			return 1
		}
		// Read exactly one line. No buffered reader may consume bytes intended for
		// a child process. The next read starts only after foreground execution ends.
		lineCh := make(chan lineResult, 1)
		go func() { line, err := readLine(in); lineCh <- lineResult{line, err} }()
		var result lineResult
	read:
		for {
			select {
			case <-ctx.Done():
				return 130
			case <-signals:
				status = 130
				fmt.Fprintln(out)
				if err := s.printPrompt(out); err != nil {
					fmt.Fprintf(errOut, "nexshell: prompt: %v\n", err)
					return 1
				}
			case result = <-lineCh:
				break read
			}
		}
		if result.err != nil && result.err != io.EOF {
			if interruptedRead(result.err) {
				status = 130
				fmt.Fprintln(out)
				continue
			}
			fmt.Fprintf(errOut, "nexshell: input: %v\n", result.err)
			return 1
		}
		if result.line == "" && result.err == io.EOF {
			fmt.Fprintln(out)
			return status
		}
		line := result.line
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		s.hist.Add(line)
		plan, err := parser.Parse(line)
		if err == nil {
			plan, err = s.expandAliases(plan)
		}
		if err != nil {
			fmt.Fprintf(errOut, "nexshell: %v\n", err)
			status = 2
			continue
		}
		commandCtx, cancel := context.WithCancel(ctx)
		shellCtx := registry.Context{CWD: &s.cwd, Aliases: s.aliases, History: s.hist, Registry: s.reg, Context: commandCtx, Stdin: in, Stdout: out, Stderr: errOut}
		type completion struct {
			status int
			err    error
		}
		completed := make(chan completion, 1)
		go func() {
			code, runErr := executor.Execute(commandCtx, plan, shellCtx)
			completed <- completion{code, runErr}
		}()
		interrupted := false
		var finished completion
	foreground:
		for {
			select {
			case <-ctx.Done():
				cancel()
				finished = <-completed
				break foreground
			case <-signals:
				interrupted = true
				cancel()
			case finished = <-completed:
				break foreground
			}
		}
		cancel()
		status = finished.status
		if interrupted {
			fmt.Fprintln(out)
		}
		var exit *registry.ExitError
		if errors.As(finished.err, &exit) {
			return exit.Code
		}
		// Executor has already routed execution diagnostics through stage stderr.
	}
}

// readLine deliberately does not read ahead. It also supports long commands
// without bufio.Scanner's 64 KiB limit. Input remains a single-line grammar.
func readLine(in io.Reader) (string, error) {
	var line strings.Builder
	var b [1]byte
	emptyReads := 0
	for {
		n, err := in.Read(b[:])
		if n > 0 {
			emptyReads = 0
			if b[0] == '\n' {
				return strings.TrimSuffix(line.String(), "\r"), nil
			}
			line.WriteByte(b[0])
		} else if err == nil {
			emptyReads++
			if emptyReads >= 100 {
				return line.String(), io.ErrNoProgress
			}
		}
		if err != nil {
			return line.String(), err
		}
	}
}

// Expand each original command once, preserving already-parsed arguments.
// Alias values may contain a pipeline, but aliases are deliberately nonrecursive.
func (s *Shell) expandAliases(plan parser.Plan) (parser.Plan, error) {
	var expanded parser.Plan
	for _, command := range plan.Commands {
		value, ok := s.aliases.Get(command.Name)
		if !ok {
			expanded.Commands = append(expanded.Commands, command)
			continue
		}
		aliasPlan, err := parser.Parse(value)
		if err != nil {
			return parser.Plan{}, fmt.Errorf("alias %q: %w", command.Name, err)
		}
		if len(aliasPlan.Commands) == 0 {
			return parser.Plan{}, fmt.Errorf("alias %q: missing command", command.Name)
		}
		last := &aliasPlan.Commands[len(aliasPlan.Commands)-1]
		last.Args = append(last.Args, command.Args...)
		for _, redirect := range command.Redirections {
			destination := last
			if redirect.Kind == parser.Input {
				destination = &aliasPlan.Commands[0]
			}
			for _, previous := range destination.Redirections {
				if sameStream(previous.Kind, redirect.Kind) {
					return parser.Plan{}, fmt.Errorf("alias %q: duplicate redirection", command.Name)
				}
			}
			destination.Redirections = append(destination.Redirections, redirect)
		}
		expanded.Commands = append(expanded.Commands, aliasPlan.Commands...)
	}
	return expanded, parser.Validate(expanded)
}

func sameStream(a, b parser.Kind) bool {
	if a == parser.Append {
		a = parser.Output
	}
	if b == parser.Append {
		b = parser.Output
	}
	return a == b
}

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiCyan   = "\033[36m"
	ansiYellow = "\033[33m"
)

func (s *Shell) printPrompt(out io.Writer) error {
	_, err := fmt.Fprintf(out, "%snexshell%s %s%s%s ❯ ", ansiCyan+ansiBold, ansiReset, ansiYellow, abbreviateHome(s.cwd), ansiReset)
	return err
}

func abbreviateHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(home, path)
	if err == nil && rel == "." {
		return "~"
	}
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		return "~" + string(filepath.Separator) + rel
	}
	return path
}
