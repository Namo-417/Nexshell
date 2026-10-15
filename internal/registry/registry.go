// Package registry provides the command registry interface and implementation
// for NexShell. Every built-in command satisfies the Command interface and
// is registered here so the REPL can dispatch to it by name.
package registry

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Context is passed to every command at run time. It provides access to
// shared shell state so built-ins can mutate cwd, aliases and history.
type Context struct {
	// Context carries cancellation for the foreground command.
	Context context.Context
	// Streams are supplied by the execution engine for pipes and redirections.
	// Nil streams retain the original process standard streams.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// CWD is a pointer to the current working directory string held in the REPL.
	CWD *string
	// Aliases is the live alias store.
	Aliases AliasStore
	// History is the live history store.
	History HistoryStore
	// Registry lets help and other meta-commands enumerate all commands.
	Registry *Registry
}

// Input returns the command's standard input, falling back to os.Stdin.
func (c Context) Input() io.Reader {
	if c.Stdin != nil {
		return c.Stdin
	}
	return os.Stdin
}

// Output returns the command's standard output, falling back to os.Stdout.
func (c Context) Output() io.Writer {
	if c.Stdout != nil {
		return c.Stdout
	}
	return os.Stdout
}

// ErrorOutput returns the command's standard error, falling back to os.Stderr.
func (c Context) ErrorOutput() io.Writer {
	if c.Stderr != nil {
		return c.Stderr
	}
	return os.Stderr
}

// Cancellation returns a non-nil context for commands that support cancellation.
func (c Context) Cancellation() context.Context {
	if c.Context != nil {
		return c.Context
	}
	return context.Background()
}

// ExitError requests a clean shell exit. The REPL, rather than a built-in,
// owns history saving and termination so execution resources can be released.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string { return fmt.Sprintf("shell exit requested (%d)", e.Code) }

// PipelineCommand optionally declares that a command can run concurrently in a
// pipeline without changing parent shell state. Undeclared commands are unsafe.
type PipelineCommand interface {
	PipelineSafe() bool
}

// AliasStore is a minimal interface so the registry package does not import alias.
type AliasStore interface {
	Set(name, value string)
	Get(name string) (string, bool)
	Delete(name string)
	All() map[string]string
}

// HistoryStore is a minimal interface so the registry package does not import history.
type HistoryStore interface {
	Add(line string)
	List() []string
	Save() error
	Load() error
}

// Command is the interface every shell command must satisfy.
type Command interface {
	// Name returns the primary name used to invoke the command.
	Name() string
	// Description returns a short one-line summary shown by `help`.
	Description() string
	// Run executes the command. args does NOT include the command name itself.
	Run(ctx Context, args []string) error
}

// Registry maps command names to Command implementations.
type Registry struct {
	commands map[string]Command
}

// New creates an empty Registry.
func New() *Registry {
	return &Registry{commands: make(map[string]Command)}
}

// Register adds a command to the registry. Panics on duplicate names so
// duplicates are caught at start-up rather than silently shadowing commands.
func (r *Registry) Register(cmd Command) {
	name := strings.TrimSpace(cmd.Name())
	if name == "" {
		panic("registry: command name must not be empty")
	}
	if _, exists := r.commands[name]; exists {
		panic(fmt.Sprintf("registry: duplicate command %q", name))
	}
	r.commands[name] = cmd
}

// Lookup returns the Command registered under name, or (nil, false).
func (r *Registry) Lookup(name string) (Command, bool) {
	cmd, ok := r.commands[name]
	return cmd, ok
}

// All returns every registered command sorted alphabetically by name.
func (r *Registry) All() []Command {
	cmds := make([]Command, 0, len(r.commands))
	for _, c := range r.commands {
		cmds = append(cmds, c)
	}
	sort.Slice(cmds, func(i, j int) bool {
		return cmds[i].Name() < cmds[j].Name()
	})
	return cmds
}
