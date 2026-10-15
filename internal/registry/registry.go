// Package registry provides the command registry interface and implementation
// for NexShell. Every built-in command satisfies the Command interface and
// is registered here so the REPL can dispatch to it by name.
package registry

import (
	"fmt"
	"sort"
	"strings"
)

// Context is passed to every command at run time. It provides access to
// shared shell state so built-ins can mutate cwd, aliases and history.
type Context struct {
	// CWD is a pointer to the current working directory string held in the REPL.
	CWD *string
	// Aliases is the live alias store.
	Aliases AliasStore
	// History is the live history store.
	History HistoryStore
	// Registry lets help and other meta-commands enumerate all commands.
	Registry *Registry
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
