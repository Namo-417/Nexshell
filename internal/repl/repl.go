// Package repl implements the NexShell Read-Eval-Print Loop.
// It reads lines from stdin, expands aliases, dispatches to built-in commands
// via the registry, and falls back to the external executor for anything else.
package repl

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"nexshell/internal/alias"
	"nexshell/internal/executor"
	"nexshell/internal/history"
	"nexshell/internal/registry"
)

// Shell holds all shared state for a running shell session.
type Shell struct {
	cwd      string
	reg      *registry.Registry
	aliases  *alias.Store
	hist     *history.Store
}

// New creates a Shell wired up with the provided sub-systems.
func New(reg *registry.Registry, aliases *alias.Store, hist *history.Store) (*Shell, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("repl: cannot get working directory: %w", err)
	}
	return &Shell{
		cwd:     cwd,
		reg:     reg,
		aliases: aliases,
		hist:    hist,
	}, nil
}

// Run starts the REPL loop. It does not return until the user exits.
func (s *Shell) Run() {
	// Load persistent history.
	if err := s.hist.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "nexshell: warning: could not load history: %v\n", err)
	}

	// Intercept Ctrl+C so the loop continues instead of exiting.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT)
	go func() {
		for range sigCh {
			fmt.Print("\n")
			s.printPrompt()
		}
	}()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		s.printPrompt()
		if !scanner.Scan() {
			// EOF (e.g. piped input finished or Ctrl+D).
			fmt.Println()
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Record in history before alias expansion.
		s.hist.Add(line)

		// Expand alias on the first token.
		line = s.aliases.Expand(line)

		// Tokenise.
		name, args := tokenise(line)
		if name == "" {
			continue
		}

		// Build the context that built-ins receive.
		ctx := registry.Context{
			CWD:      &s.cwd,
			Aliases:  s.aliases,
			History:  s.hist,
			Registry: s.reg,
		}

		// Dispatch: built-in first, then external.
		if cmd, ok := s.reg.Lookup(name); ok {
			if err := cmd.Run(ctx, args); err != nil {
				printError(err)
			}
		} else {
			if err := executor.Run(name, args); err != nil {
				printError(err)
			}
		}
	}

	// Save history on clean exit.
	_ = s.hist.Save()
}

// ─── prompt ──────────────────────────────────────────────────────────────────

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiCyan   = "\033[36m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
)

func (s *Shell) printPrompt() {
	dir := abbreviateHome(s.cwd)
	if runtime.GOOS == "windows" {
		fmt.Printf("%snexshell%s %s%s%s ❯ ",
			ansiCyan+ansiBold, ansiReset,
			ansiYellow, dir, ansiReset)
	} else {
		fmt.Printf("%snexshell%s %s%s%s ❯ ",
			ansiCyan+ansiBold, ansiReset,
			ansiYellow, dir, ansiReset)
	}
}

func abbreviateHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// ─── tokeniser ───────────────────────────────────────────────────────────────

// tokenise splits a raw input line into (commandName, args).
// It handles single-quoted and double-quoted groups as single tokens.
func tokenise(line string) (string, []string) {
	var tokens []string
	var cur strings.Builder
	inSingle := false
	inDouble := false

	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		case ch == ' ' && !inSingle && !inDouble:
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(ch)
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	if len(tokens) == 0 {
		return "", nil
	}
	return tokens[0], tokens[1:]
}

// ─── error printing ──────────────────────────────────────────────────────────

func printError(err error) {
	var notFound *executor.NotFoundError
	var execErr *executor.ExecError
	switch {
	case errors.As(err, &notFound):
		fmt.Fprintf(os.Stderr, "%s%s%s\n", "\033[31m", err.Error(), ansiReset)
	case errors.As(err, &execErr):
		// Non-zero exit — already printed by the child's stderr; just show code.
		fmt.Fprintf(os.Stderr, "%sexit status %d%s\n", "\033[33m", execErr.ExitCode, ansiReset)
	default:
		fmt.Fprintf(os.Stderr, "%s%s%s\n", "\033[31m", err.Error(), ansiReset)
	}
}
